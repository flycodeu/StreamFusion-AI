package com.streamfusion.platform.camera.ingress;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import jakarta.annotation.PreDestroy;
import java.io.IOException;
import java.nio.channels.*;
import java.nio.file.*;
import java.util.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

/** Write-ahead ownership of exact RTP receivers. No source URLs, SIP passwords or ZLM secrets. */
@Component
public final class GbRtpJournal implements AutoCloseable {
    private final Path file;
    private final ObjectMapper json;
    private LinkedHashMap<String, Entry> entries;
    private FileChannel channel;
    private FileLock lock;
    private boolean closed;

    @Autowired
    public GbRtpJournal(PlatformProperties properties, ObjectMapper json) {
        this(properties.workDir(), json);
    }

    GbRtpJournal(Path workDir, ObjectMapper json) {
        file = workDir.resolve("camera-ingress-rtp.json");
        this.json = json;
    }

    synchronized List<Entry> entries() {
        load();
        return List.copyOf(entries.values());
    }

    synchronized void register(Entry entry) {
        load();
        if (!entry.valid() || entries.size() >= 64 || entries.containsKey(entry.key()))
            throw unavailable();
        var next = new LinkedHashMap<>(entries);
        next.put(entry.key(), entry);
        persist(next);
    }

    synchronized void exited(Entry entry) {
        load();
        var next = new LinkedHashMap<>(entries);
        next.remove(entry.key());
        persist(next);
    }

    private void load() {
        if (closed) throw unavailable();
        if (entries != null) return;
        try {
            Files.createDirectories(file.getParent());
            if (!Files.isDirectory(file.getParent(), LinkOption.NOFOLLOW_LINKS))
                throw new IOException();
            channel =
                    FileChannel.open(
                            file.resolveSibling("camera-ingress-rtp.lock"),
                            StandardOpenOption.CREATE,
                            StandardOpenOption.WRITE,
                            LinkOption.NOFOLLOW_LINKS);
            lock = channel.tryLock();
            if (lock == null) throw new IOException();
            var loaded = new LinkedHashMap<String, Entry>();
            if (Files.exists(file, LinkOption.NOFOLLOW_LINKS)) {
                if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS)
                        || Files.size(file) > 262144) throw new IOException();
                var values = json.readValue(file.toFile(), Entry[].class);
                if (values == null || values.length > 64) throw new IOException();
                for (var entry : values)
                    if (entry == null || !entry.valid() || loaded.put(entry.key(), entry) != null)
                        throw new IOException();
            }
            entries = loaded;
        } catch (IOException | RuntimeException failure) {
            release();
            throw unavailable();
        }
    }

    private void persist(LinkedHashMap<String, Entry> next) {
        Path temporary = null;
        try {
            if (lock == null || !lock.isValid() || Files.isSymbolicLink(file))
                throw new IOException();
            byte[] bytes = json.writeValueAsBytes(next.values());
            if (bytes.length > 262144) throw new IOException();
            temporary = Files.createTempFile(file.getParent(), "gb-rtp-", ".tmp");
            try (var output = FileChannel.open(temporary, StandardOpenOption.WRITE)) {
                var buffer = java.nio.ByteBuffer.wrap(bytes);
                while (buffer.hasRemaining()) output.write(buffer);
                output.force(true);
            }
            Files.move(
                    temporary,
                    file,
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING);
            entries = next;
        } catch (IOException failure) {
            throw unavailable();
        } finally {
            if (temporary != null)
                try {
                    Files.deleteIfExists(temporary);
                } catch (IOException ignored) {
                }
        }
    }

    private static PreviewFailure unavailable() {
        return new PreviewFailure("GB_RECOVERY_REQUIRED");
    }

    private void release() {
        try {
            if (lock != null) lock.release();
        } catch (IOException ignored) {
        }
        try {
            if (channel != null) channel.close();
        } catch (IOException ignored) {
        }
        lock = null;
        channel = null;
    }

    @PreDestroy
    public synchronized void close() {
        closed = true;
        release();
    }

    record Entry(String api, String config, String app, String id) {
        static Entry of(ActiveZlm runtime, String app, String id) {
            return new Entry(
                    runtime.apiBase().toString(),
                    runtime.configPath().toAbsolutePath().normalize().toString(),
                    app,
                    id);
        }

        String key() {
            return api + "\n" + config + "\n" + app + "/" + id;
        }

        boolean valid() {
            return api != null
                    && api.length() < 4096
                    && config != null
                    && config.length() < 4096
                    && app != null
                    && app.matches("sf_gb_[a-f0-9]{12}")
                    && id != null
                    && id.matches("[a-f0-9]{32}");
        }

        boolean matches(ActiveZlm runtime) {
            return api.equals(runtime.apiBase().toString())
                    && config.equals(runtime.configPath().toAbsolutePath().normalize().toString());
        }
    }
}
