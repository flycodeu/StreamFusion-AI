package com.streamfusion.platform.monitor.diagnostics;

import static com.streamfusion.platform.common.exception.BusinessException.error;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.channels.SeekableByteChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDate;
import java.time.ZoneId;
import java.util.*;
import java.util.concurrent.Semaphore;
import org.springframework.stereotype.Service;

@Service
public class DiagnosticQueryService {
    private static final int MAX_BYTES = 16 * 1024 * 1024;
    private static final int MAX_LINE = 64 * 1024;
    private static final int LIMIT = 200;
    private static final ZoneId LOG_ZONE = ZoneId.of("Asia/Shanghai");
    private final Path directory;
    private final ObjectMapper json;
    private final Clock clock;
    private final Semaphore readers = new Semaphore(2);

    public DiagnosticQueryService(PlatformProperties properties, ObjectMapper json, Clock clock) {
        this.directory = properties.workDir().resolve("logs");
        this.json = json;
        this.clock = clock;
    }

    public record Query(
            Instant startTime,
            Instant endTime,
            String traceId,
            String planId,
            String checkId,
            String kind,
            String level) {}

    public record Result(
            List<DiagnosticEvent> items,
            boolean truncated,
            boolean unavailable,
            int retentionDays,
            int limit) {}

    public Result query(Query query) {
        Instant end = query.endTime() == null ? clock.instant() : query.endTime();
        Instant start =
                query.startTime() == null ? end.minus(Duration.ofDays(1)) : query.startTime();
        if (start.isAfter(end)
                || Duration.between(start, end).compareTo(Duration.ofDays(7)) > 0
                || !matches(query.traceId(), "[a-f0-9]{32}")
                || !matches(query.planId(), "[1-9][0-9]{0,18}")
                || !matches(query.checkId(), "[1-9][0-9]{0,18}")
                || !matches(query.kind(), "REQUEST|CAMERA_CHECK")
                || !matches(query.level(), "INFO|WARN|ERROR"))
            throw error(ErrorCode.VALIDATION_ERROR);
        if (!readers.tryAcquire()) throw error(ErrorCode.RATE_LIMITED);
        try {
            return scan(query, start, end);
        } finally {
            readers.release();
        }
    }

    private Result scan(Query query, Instant start, Instant end) {
        var items = new ArrayList<DiagnosticEvent>();
        boolean truncated = false;
        boolean unavailable = false;
        long deadline = System.nanoTime() + Duration.ofSeconds(2).toNanos();
        int remaining = MAX_BYTES;
        var files = new ArrayList<Path>();
        if (!Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS))
            return new Result(List.of(), false, true, 7, LIMIT);
        try (var paths = Files.newDirectoryStream(directory, "diagnostics*.jsonl")) {
            for (Path path : paths) {
                String name = path.getFileName().toString();
                if (name.matches("diagnostics(?:\\.\\d{4}-\\d{2}-\\d{2}\\.\\d{1,5})?\\.jsonl"))
                    files.add(path);
                if (files.size() >= 1024 || System.nanoTime() > deadline) {
                    truncated = true;
                    break;
                }
            }
        } catch (IOException ex) {
            unavailable = true;
        }
        files.sort(Comparator.comparing(DiagnosticQueryService::sortKey).reversed());
        for (Path file : files) {
            if (!file.getFileName().toString().equals("diagnostics.jsonl")) {
                try {
                    LocalDate date = LocalDate.parse(file.getFileName().toString().split("\\.")[1]);
                    if (date.isBefore(start.atZone(LOG_ZONE).toLocalDate())
                            || date.isAfter(end.atZone(LOG_ZONE).toLocalDate())) continue;
                } catch (java.time.DateTimeException ex) {
                    unavailable = true;
                    continue;
                }
            }
            if (remaining <= 0 || items.size() >= LIMIT || System.nanoTime() > deadline) {
                truncated = true;
                break;
            }
            if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS)) {
                unavailable = true;
                continue;
            }
            try (SeekableByteChannel channel =
                    Files.newByteChannel(
                            file, StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS)) {
                long size = channel.size();
                int length = (int) Math.min(size, remaining);
                byte[] bytes = new byte[length];
                channel.position(size - length);
                var buffer = ByteBuffer.wrap(bytes);
                while (buffer.hasRemaining() && channel.read(buffer) > 0) {
                    if (System.nanoTime() > deadline) break;
                }
                remaining -= length;
                if (length < size || buffer.hasRemaining()) truncated = true;
                int lineEnd = buffer.position() - 1;
                // Ignore a partially written final record and a partially read first record.
                while (lineEnd >= 0 && bytes[lineEnd] != '\n') lineEnd--;
                for (int index = lineEnd - 1; index >= -1; index--) {
                    if (System.nanoTime() > deadline || items.size() >= LIMIT) {
                        truncated = true;
                        break;
                    }
                    if (index >= 0 && bytes[index] != '\n') continue;
                    int lineLength = lineEnd - index - 1;
                    if ((index >= 0 || size == length)
                            && lineLength > 0
                            && lineLength <= MAX_LINE) {
                        try {
                            var event =
                                    json.readValue(
                                            new String(
                                                    bytes,
                                                    index + 1,
                                                    lineLength,
                                                    StandardCharsets.UTF_8),
                                            DiagnosticEvent.class);
                            if (event.occurredAt() != null
                                    && !event.occurredAt().isBefore(start)
                                    && !event.occurredAt().isAfter(end)
                                    && selected(query, event)) items.add(event);
                        } catch (IOException | RuntimeException ex) {
                            unavailable = true;
                        }
                    } else if (lineLength > MAX_LINE) unavailable = true;
                    lineEnd = index;
                }
            } catch (IOException ex) {
                unavailable = true;
            }
        }
        items.sort(Comparator.comparing(DiagnosticEvent::occurredAt).reversed());
        return new Result(List.copyOf(items), truncated, unavailable, 7, LIMIT);
    }

    private static String sortKey(Path path) {
        String name = path.getFileName().toString();
        if (name.equals("diagnostics.jsonl")) return "z";
        String[] parts = name.split("\\.");
        return parts[1] + String.format(Locale.ROOT, "%05d", Integer.parseInt(parts[2]));
    }

    private static boolean selected(Query query, DiagnosticEvent event) {
        return equal(query.traceId(), event.traceId())
                && equal(query.planId(), event.planId())
                && equal(query.checkId(), event.checkId())
                && equal(query.kind(), event.kind())
                && equal(query.level(), event.level());
    }

    private static boolean equal(String filter, String value) {
        return filter == null || filter.isBlank() || filter.equals(value);
    }

    private static boolean matches(String value, String pattern) {
        return value == null || value.isEmpty() || value.matches(pattern);
    }
}
