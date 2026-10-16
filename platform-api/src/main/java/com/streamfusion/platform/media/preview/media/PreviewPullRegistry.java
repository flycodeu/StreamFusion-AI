package com.streamfusion.platform.media.preview.media;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import jakarta.annotation.PreDestroy;
import java.io.IOException;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.*;
import java.time.Instant;
import java.util.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

/** Exact preview ownership, persisted before creation. No sessions, camera URLs or credentials. */
@Component
public final class PreviewPullRegistry implements AutoCloseable {
    private static final int MAX_ENTRIES = 256;
    private final Path file;
    private final ObjectMapper json;
    private final Set<String> current = new HashSet<>();
    private LinkedHashMap<String, Entry> entries;
    private FileChannel channel;
    private FileLock lock;
    private boolean closed;

    @Autowired
    public PreviewPullRegistry(PlatformProperties properties, ObjectMapper json) {
        this(properties.workDir(), json);
    }

    public PreviewPullRegistry(Path directory, ObjectMapper json) {
        file = directory.resolve("preview-pulls.json");
        this.json = json;
    }

    public synchronized void requireRecovered() {
        load();
        if (entries.keySet().stream().anyMatch(key -> !current.contains(key))) throw unavailable();
    }

    public synchronized void register(Stream stream, boolean viewer) {
        load();
        var entry =
                new Entry(
                        stream.runtime().apiBase().toString(),
                        stream.runtime().configPath().toAbsolutePath().normalize().toString(),
                        stream.app(),
                        stream.id(),
                        viewer,
                        Instant.now().plusSeconds(30).toEpochMilli());
        if (!entry.valid()
                || !stream.proxyKey().equals("__defaultVhost__/" + stream.app() + "/" + stream.id())
                || entries.containsKey(entry.key())
                || entries.size() >= MAX_ENTRIES) throw unavailable();
        var next = new LinkedHashMap<>(entries);
        next.put(entry.key(), entry);
        persist(next);
        current.add(entry.key());
    }

    public synchronized void started(Stream stream) {
        load();
        var key = key(stream);
        var entry = entries.get(key);
        if (entry == null || !current.contains(key)) throw unavailable();
        var next = new LinkedHashMap<>(entries);
        next.put(
                key,
                new Entry(entry.api(), entry.config(), entry.app(), entry.id(), entry.viewer(), 0));
        persist(next);
    }

    /** Called only after the gateway has confirmed the exact resources stopped. */
    public synchronized void exited(Stream stream) {
        load();
        var key = key(stream);
        var entry = entries.get(key);
        if (entry == null) return;
        if (Instant.now().toEpochMilli() < entry.notBefore()) throw unavailable();
        var next = new LinkedHashMap<>(entries);
        next.remove(key);
        persist(next);
        current.remove(key);
    }

    public synchronized List<Entry> pending() {
        load();
        return entries.values().stream()
                .filter(e -> !current.contains(e.key()))
                .sorted(Comparator.comparing(Entry::viewer).reversed())
                .toList();
    }

    private static String key(Stream stream) {
        return stream.runtime().apiBase()
                + "\n"
                + stream.runtime().configPath().toAbsolutePath().normalize()
                + "\n"
                + stream.app()
                + "/"
                + stream.id();
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
                            file.resolveSibling("preview-pulls.lock"),
                            StandardOpenOption.CREATE,
                            StandardOpenOption.WRITE,
                            LinkOption.NOFOLLOW_LINKS);
            lock = channel.tryLock();
            if (lock == null) throw new IOException();
            var loaded = new LinkedHashMap<String, Entry>();
            if (Files.exists(file, LinkOption.NOFOLLOW_LINKS)) {
                if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS)
                        || Files.size(file) > 1024 * 1024) throw new IOException();
                Entry[] values = json.readValue(file.toFile(), Entry[].class);
                if (values == null || values.length > MAX_ENTRIES) throw new IOException();
                for (var entry : values)
                    if (entry == null || !entry.valid() || loaded.put(entry.key(), entry) != null)
                        throw new IOException();
            }
            entries = loaded;
        } catch (IOException | RuntimeException ex) {
            releaseLock();
            throw unavailable();
        }
    }

    private void persist(LinkedHashMap<String, Entry> next) {
        if (closed || lock == null || !lock.isValid()) throw unavailable();
        Path temporary = null;
        try {
            if (Files.isSymbolicLink(file)) throw new IOException();
            byte[] bytes = json.writeValueAsBytes(next.values());
            if (bytes.length > 1024 * 1024) throw new IOException();
            temporary = Files.createTempFile(file.getParent(), "preview-pulls-", ".tmp");
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
        } catch (IOException ex) {
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
        return new PreviewFailure("PREVIEW_RECOVERY_REQUIRED");
    }

    @Override
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
            if (channel != null) channel.close();
        } catch (IOException ignored) {
        }
        lock = null;
        channel = null;
    }

    public record Entry(
            String api, String config, String app, String id, boolean viewer, long notBefore) {
        String key() {
            return api + "\n" + config + "\n" + app + "/" + id;
        }

        public boolean matches(ActiveZlm runtime) {
            return api.equals(runtime.apiBase().toString())
                    && config.equals(runtime.configPath().toAbsolutePath().normalize().toString());
        }

        boolean valid() {
            return api != null
                    && api.length() < 4096
                    && config != null
                    && config.length() < 4096
                    && app != null
                    && app.matches("sf_preview_[a-f0-9]{12}")
                    && id != null
                    && id.matches("[a-f0-9]{32}")
                    && notBefore >= 0;
        }
    }
}
