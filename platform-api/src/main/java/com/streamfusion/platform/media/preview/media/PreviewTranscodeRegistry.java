package com.streamfusion.platform.media.preview.media;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import jakarta.annotation.PreDestroy;
import java.io.IOException;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.time.Instant;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Optional;
import java.util.Set;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

/**
 * Owned resources survive unknown start/stop results and platform restarts. No media credentials
 * are stored.
 */
@Component
public final class PreviewTranscodeRegistry {
    private static final int MAX_JOURNAL_BYTES = 4 * 1024 * 1024;
    private final Path file;
    private final ObjectMapper json;
    private final int maxTranscodes;
    private final Set<String> current = new HashSet<>();
    private LinkedHashMap<String, Entry> entries;
    private FileChannel lockChannel;
    private FileLock lock;
    private boolean closed;

    @Autowired
    public PreviewTranscodeRegistry(
            PlatformProperties platform, ObjectMapper json, PreviewProperties limits) {
        this(platform.workDir(), json, limits.maxTranscodes());
    }

    PreviewTranscodeRegistry(Path directory, ObjectMapper json) {
        this(directory, json, 64);
    }

    public PreviewTranscodeRegistry(Path directory, ObjectMapper json, int maxTranscodes) {
        if (maxTranscodes < 0) throw new IllegalArgumentException("Invalid transcode capacity");
        this.file = directory.resolve("preview-transcodes.json");
        this.json = json;
        this.maxTranscodes = maxTranscodes;
    }

    synchronized void register(
            PreviewMediaGateway.Stream source, PreviewMediaGateway.Transcode output) {
        load();
        if (entries.containsKey(output.key()))
            throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        if (maxTranscodes == 0) throw new PreviewFailure("TRANSCODE_DISABLED");
        if (entries.size() >= maxTranscodes)
            throw new PreviewFailure("TRANSCODE_CAPACITY_EXCEEDED");
        var next = new LinkedHashMap<>(entries);
        next.put(
                output.key(),
                new Entry(
                        source.runtime().apiBase().toString(),
                        source.runtime().configPath().toAbsolutePath().normalize().toString(),
                        source.app(),
                        source.id(),
                        output.id(),
                        output.key(),
                        Instant.now().plusSeconds(30).toEpochMilli()));
        persist(next);
        current.add(output.key());
    }

    synchronized void started(String key) {
        load();
        Entry entry = entries.get(key);
        if (entry == null) throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        var next = new LinkedHashMap<>(entries);
        next.put(
                key,
                new Entry(
                        entry.api(),
                        entry.config(),
                        entry.app(),
                        entry.input(),
                        entry.output(),
                        entry.key(),
                        0));
        persist(next);
    }

    synchronized boolean canConfirmExit(String key) {
        load();
        Entry entry = entries.get(key);
        return entry == null || Instant.now().toEpochMilli() >= entry.notBefore();
    }

    synchronized void releasing(String key) {
        load();
        current.remove(key);
    }

    synchronized void exited(String key) {
        load();
        if (!entries.containsKey(key)) return;
        var next = new LinkedHashMap<>(entries);
        next.remove(key);
        persist(next);
        current.remove(key);
    }

    synchronized Set<String> keys() {
        load();
        return Set.copyOf(entries.keySet());
    }

    /** Includes old-runtime and unknown-start records until exit has been confirmed. */
    public synchronized int occupied() {
        load();
        return entries.size();
    }

    synchronized List<Entry> recoverable(ActiveZlm runtime) {
        load();
        return entries.values().stream()
                .filter(entry -> !current.contains(entry.key()))
                .filter(
                        entry ->
                                entry.api().equals(runtime.apiBase().toString())
                                        && entry.config()
                                                .equals(
                                                        runtime.configPath()
                                                                .toAbsolutePath()
                                                                .normalize()
                                                                .toString()))
                .toList();
    }

    synchronized Optional<Entry> nextRecoverable(ActiveZlm runtime) {
        load();
        String api = runtime.apiBase().toString();
        String config = runtime.configPath().toAbsolutePath().normalize().toString();
        long now = Instant.now().toEpochMilli();
        Entry selected = null;
        var iterator = entries.values().iterator();
        while (iterator.hasNext()) {
            Entry entry = iterator.next();
            if (current.contains(entry.key())
                    || now < entry.notBefore()
                    || !entry.api().equals(api)
                    || !entry.config().equals(config)) continue;
            selected = entry;
            iterator.remove();
            break;
        }
        // Rotate in memory only; a failed cleanup cannot starve later entries.
        if (selected != null) entries.put(selected.key(), selected);
        return Optional.ofNullable(selected);
    }

    private void load() {
        if (closed) throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        if (entries != null) return;
        try {
            Files.createDirectories(file.getParent());
            if (!Files.isDirectory(file.getParent(), LinkOption.NOFOLLOW_LINKS))
                throw new IOException();
            var lockFile = file.resolveSibling("preview-transcodes.lock");
            lockChannel =
                    FileChannel.open(
                            lockFile,
                            StandardOpenOption.CREATE,
                            StandardOpenOption.WRITE,
                            LinkOption.NOFOLLOW_LINKS);
            lock = lockChannel.tryLock();
            if (lock == null) throw new IOException();
            var loaded = new LinkedHashMap<String, Entry>();
            if (Files.exists(file, LinkOption.NOFOLLOW_LINKS)) {
                if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS)
                        || Files.size(file) > MAX_JOURNAL_BYTES) throw new IOException();
                Entry[] values = json.readValue(file.toFile(), Entry[].class);
                if (values == null) throw new IOException();
                for (Entry entry : values) {
                    if (entry == null || !entry.valid() || loaded.put(entry.key(), entry) != null)
                        throw new IOException();
                }
            }
            entries = loaded;
        } catch (IOException | RuntimeException failure) {
            releaseLock();
            throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        }
    }

    private void persist(LinkedHashMap<String, Entry> next) {
        if (closed || lock == null || !lock.isValid())
            throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        Path temporary = null;
        try {
            if (Files.isSymbolicLink(file)) throw new IOException();
            temporary = Files.createTempFile(file.getParent(), "preview-transcodes-", ".tmp");
            byte[] bytes = json.writeValueAsBytes(next.values());
            if (bytes.length > MAX_JOURNAL_BYTES) throw new IOException();
            try (var channel = FileChannel.open(temporary, StandardOpenOption.WRITE)) {
                var buffer = java.nio.ByteBuffer.wrap(bytes);
                while (buffer.hasRemaining()) channel.write(buffer);
                channel.force(true);
            }
            Files.move(
                    temporary,
                    file,
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING);
            entries = next;
        } catch (IOException failure) {
            throw new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED");
        } finally {
            if (temporary != null) {
                try {
                    Files.deleteIfExists(temporary);
                } catch (IOException ignored) {
                }
            }
        }
    }

    @PreDestroy
    public synchronized void close() {
        closed = true;
        releaseLock();
    }

    private void releaseLock() {
        try {
            if (lock != null) lock.release();
        } catch (IOException ignored) {
        }
        try {
            if (lockChannel != null) lockChannel.close();
        } catch (IOException ignored) {
        }
        lock = null;
        lockChannel = null;
    }

    public record Entry(
            String api,
            String config,
            String app,
            String input,
            String output,
            String key,
            long notBefore) {
        boolean valid() {
            return api != null
                    && api.length() < 4096
                    && config != null
                    && config.length() < 4096
                    && app != null
                    && app.matches("sf_preview_[a-f0-9]{12}")
                    && input != null
                    && input.matches("[a-f0-9]{32}")
                    && (input + "h264").equals(output)
                    && key != null
                    && key.matches("[a-f0-9]{32}")
                    && notBefore >= 0;
        }
    }
}
