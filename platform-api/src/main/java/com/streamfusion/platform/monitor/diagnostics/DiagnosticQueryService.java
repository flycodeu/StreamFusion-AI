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
import java.nio.file.attribute.BasicFileAttributes;
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
            String level,
            Integer size,
            String cursor) {
        public Query(
                Instant startTime,
                Instant endTime,
                String traceId,
                String planId,
                String checkId,
                String kind,
                String level) {
            this(startTime, endTime, traceId, planId, checkId, kind, level, null, null);
        }
    }

    private record Cursor(String fileId, long offset) {}

    public record Result(
            List<DiagnosticEvent> items,
            boolean truncated,
            boolean unavailable,
            int retentionDays,
            int limit,
            String nextCursor,
            boolean cursorExpired,
            long droppedEvents,
            long total,
            boolean totalExact) {}

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
                || !matches(query.level(), "INFO|WARN|ERROR")
                || query.size() != null && (query.size() < 1 || query.size() > LIMIT))
            throw error(ErrorCode.VALIDATION_ERROR);
        Cursor cursor = decode(query.cursor());
        if (!readers.tryAcquire()) throw error(ErrorCode.RATE_LIMITED);
        try {
            var page = scan(query, start, end, cursor, false);
            var count =
                    cursor == null && page.totalExact()
                            ? page
                            : scan(query, start, end, null, true);
            return new Result(
                    page.items(),
                    page.truncated(),
                    page.unavailable(),
                    page.retentionDays(),
                    page.limit(),
                    page.nextCursor(),
                    page.cursorExpired(),
                    page.droppedEvents(),
                    count.total(),
                    count.totalExact());
        } finally {
            readers.release();
        }
    }

    private Result scan(Query query, Instant start, Instant end, Cursor cursor, boolean countOnly) {
        var items = new ArrayList<DiagnosticEvent>();
        long matched = 0;
        boolean truncated = false;
        boolean unavailable = false;
        long deadline = System.nanoTime() + Duration.ofSeconds(2).toNanos();
        int remaining = MAX_BYTES;
        int limit = countOnly ? Integer.MAX_VALUE : query.size() == null ? LIMIT : query.size();
        String nextCursor = null;
        var files = new ArrayList<Path>();
        if (!Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS))
            return result(List.of(), false, true, limit, null, cursor != null, 0);
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
        boolean found = cursor == null;
        for (Path file : files) {
            String fileId;
            try {
                var attributes =
                        Files.readAttributes(
                                file, BasicFileAttributes.class, LinkOption.NOFOLLOW_LINKS);
                if (!attributes.isRegularFile()) {
                    unavailable = true;
                    continue;
                }
                // File identity survives rolling rename; never accept a client-supplied path.
                fileId = fileIdentity(file, attributes);
            } catch (IOException ex) {
                unavailable = true;
                continue;
            }
            boolean resume = !found && cursor.fileId().equals(fileId);
            if (!found && !resume) continue;
            found = true;
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
            if (remaining <= 0 || items.size() >= limit || System.nanoTime() > deadline) {
                truncated = items.size() < limit;
                nextCursor = encode(new Cursor(fileId, resume ? cursor.offset() : Long.MAX_VALUE));
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
                long position = resume ? Math.min(cursor.offset(), size) : size;
                if (resume && cursor.offset() != Long.MAX_VALUE && cursor.offset() > size)
                    return result(List.of(), false, true, limit, null, true, 0);
                while (position > 0) {
                    if (remaining <= 0 || System.nanoTime() > deadline || items.size() >= limit) {
                        truncated = items.size() < limit;
                        nextCursor = encode(new Cursor(fileId, position));
                        break;
                    }
                    int length = (int) Math.min(position, Math.min(1024 * 1024, remaining));
                    long blockStart = position - length;
                    byte[] bytes = new byte[length];
                    channel.position(blockStart);
                    var buffer = ByteBuffer.wrap(bytes);
                    while (buffer.hasRemaining() && channel.read(buffer) > 0) {
                        if (System.nanoTime() > deadline) break;
                    }
                    remaining -= length;
                    if (buffer.hasRemaining()) {
                        unavailable = true;
                        return result(
                                items,
                                true,
                                true,
                                limit,
                                encode(new Cursor(fileId, position)),
                                false,
                                matched);
                    }
                    int lineEnd = buffer.position() - 1;
                    // Ignore a partially written final record and a partially read first record.
                    while (lineEnd >= 0 && bytes[lineEnd] != '\n') lineEnd--;
                    for (int index = lineEnd - 1; index >= -1; index--) {
                        if (index >= 0 && bytes[index] != '\n') continue;
                        int lineLength = lineEnd - index - 1;
                        if ((index >= 0 || blockStart == 0)
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
                                        && selected(query, event)) {
                                    matched++;
                                    if (!countOnly) items.add(event);
                                }
                            } catch (IOException | RuntimeException ex) {
                                unavailable = true;
                            }
                        } else if (lineLength > MAX_LINE) unavailable = true;
                        lineEnd = index;
                        if (index >= 0 || blockStart == 0) position = blockStart + index + 1;
                        if (items.size() >= limit || System.nanoTime() > deadline) break;
                    }
                    if (position == blockStart + length) {
                        if (blockStart > 0 && length <= MAX_LINE) {
                            // A budget boundary can cut a valid record. Retry it on the next page.
                            nextCursor = encode(new Cursor(fileId, position));
                            truncated = true;
                            break;
                        }
                        // An oversized/incomplete line must not prevent forward progress.
                        position = blockStart;
                        unavailable = true;
                    }
                }
                if (nextCursor != null) break;
            } catch (IOException ex) {
                unavailable = true;
            }
        }
        return result(items, truncated, unavailable, limit, nextCursor, !found, matched);
    }

    private static Result result(
            List<DiagnosticEvent> items,
            boolean truncated,
            boolean unavailable,
            int limit,
            String nextCursor,
            boolean cursorExpired,
            long matched) {
        return new Result(
                List.copyOf(items),
                truncated,
                unavailable,
                7,
                limit,
                nextCursor,
                cursorExpired,
                DiagnosticAsyncAppender.droppedEvents(),
                matched,
                !truncated && !unavailable && nextCursor == null && !cursorExpired);
    }

    private static String encode(Cursor cursor) {
        return Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(
                        (cursor.offset() + "\n" + cursor.fileId())
                                .getBytes(StandardCharsets.UTF_8));
    }

    private static String fileIdentity(Path file, BasicFileAttributes attributes)
            throws IOException {
        if (attributes.fileKey() != null) return attributes.fileKey().toString();
        // Windows may return no fileKey and reuse creation times when a rolled name is recreated.
        // Include a bounded content fingerprint, stable while complete records are appended.
        try (var channel =
                Files.newByteChannel(file, StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS)) {
            var head = ByteBuffer.allocate((int) Math.min(256, channel.size()));
            while (head.hasRemaining() && channel.read(head) > 0) {}
            int length = head.position();
            for (int i = 0; i < length; i++)
                if (head.array()[i] == '\n') {
                    length = i + 1;
                    break;
                }
            try {
                var digest = java.security.MessageDigest.getInstance("SHA-256");
                digest.update(head.array(), 0, length);
                return attributes.creationTime() + ":" + HexFormat.of().formatHex(digest.digest());
            } catch (java.security.NoSuchAlgorithmException ex) {
                throw new IllegalStateException(ex);
            }
        }
    }

    private static Cursor decode(String value) {
        if (value == null || value.isEmpty()) return null;
        try {
            if (value.length() > 1024) throw new IllegalArgumentException();
            String[] parts =
                    new String(Base64.getUrlDecoder().decode(value), StandardCharsets.UTF_8)
                            .split("\n", 2);
            long offset = Long.parseLong(parts[0]);
            if (offset < 0 || parts.length != 2 || parts[1].isBlank())
                throw new IllegalArgumentException();
            return new Cursor(parts[1], offset);
        } catch (RuntimeException ex) {
            throw error(ErrorCode.VALIDATION_ERROR);
        }
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
