package com.streamfusion.platform.camera.status;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
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
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

/** One durable slot, held until an owned pull is confirmed stopped. Never stores source URLs. */
@Component
public final class CameraMediaPullRegistry implements AutoCloseable {
    private final Path file;
    private final ObjectMapper json;
    private FileChannel channel;
    private FileLock lock;
    private Entry entry;
    private boolean loaded;
    private boolean current;
    private boolean closed;
    private CompletableFuture<Void> stopped;
    private ActiveZlm ownedRuntime;

    @Autowired
    public CameraMediaPullRegistry(PlatformProperties platform, ObjectMapper json) {
        this(platform.workDir(), json);
    }

    CameraMediaPullRegistry(Path directory, ObjectMapper json) {
        this.file = directory.resolve("camera-media-pull.json");
        this.json = json;
    }

    synchronized void reserve(
            Stream stream, long checkId, String traceId, CompletableFuture<Void> completion) {
        load();
        if (entry != null)
            throw new PreviewFailure(current ? "MEDIA_BUSY" : "MEDIA_RECOVERY_REQUIRED");
        Entry next =
                new Entry(
                        stream.runtime().apiBase().toString(),
                        stream.runtime().configPath().toAbsolutePath().normalize().toString(),
                        stream.app(),
                        stream.id(),
                        Instant.now().plusSeconds(30).toEpochMilli(),
                        checkId,
                        traceId);
        if (!next.valid()) throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        persist(next);
        current = true;
        stopped = completion;
        ownedRuntime = stream.runtime();
    }

    synchronized void started() {
        load();
        if (entry == null || !current) throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        persist(
                new Entry(
                        entry.api(),
                        entry.config(),
                        entry.app(),
                        entry.id(),
                        0,
                        entry.checkId(),
                        entry.traceId()));
    }

    synchronized void releasing() {
        current = false;
    }

    synchronized boolean hasPending() {
        load();
        return entry != null && !current;
    }

    synchronized Optional<ActiveZlm> ownedRuntime() {
        load();
        return Optional.ofNullable(ownedRuntime);
    }

    synchronized void notLaunched() {
        if (!current) throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        try {
            persist(null);
        } finally {
            current = false;
        }
        var completed = stopped;
        stopped = null;
        ownedRuntime = null;
        if (completed != null) completed.complete(null);
    }

    synchronized Optional<Entry> recoverable(ActiveZlm runtime) {
        load();
        if (entry == null
                || current
                || Instant.now().toEpochMilli() < entry.notBefore()
                || !entry.api().equals(runtime.apiBase().toString())
                || !entry.config()
                        .equals(runtime.configPath().toAbsolutePath().normalize().toString()))
            return Optional.empty();
        return Optional.of(entry);
    }

    synchronized void exited(Stream stream) {
        load();
        if (entry == null
                || current
                || !entry.app().equals(stream.app())
                || !entry.id().equals(stream.id())
                || !entry.api().equals(stream.runtime().apiBase().toString())
                || !entry.config()
                        .equals(
                                stream.runtime()
                                        .configPath()
                                        .toAbsolutePath()
                                        .normalize()
                                        .toString()))
            throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        persist(null);
        var completed = stopped;
        stopped = null;
        ownedRuntime = null;
        if (completed != null) completed.complete(null);
    }

    private void load() {
        if (closed) throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        if (loaded) return;
        try {
            Files.createDirectories(file.getParent());
            if (!Files.isDirectory(file.getParent(), LinkOption.NOFOLLOW_LINKS))
                throw new IOException();
            channel =
                    FileChannel.open(
                            file.resolveSibling("camera-media-pull.lock"),
                            StandardOpenOption.CREATE,
                            StandardOpenOption.WRITE,
                            LinkOption.NOFOLLOW_LINKS);
            lock = channel.tryLock();
            if (lock == null) throw new IOException();
            if (Files.exists(file, LinkOption.NOFOLLOW_LINKS)) {
                if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS)
                        || Files.size(file) > 16_384) throw new IOException();
                Entry[] values = json.readValue(file.toFile(), Entry[].class);
                if (values == null || values.length > 1) throw new IOException();
                if (values.length == 1) {
                    if (values[0] == null || !values[0].valid()) throw new IOException();
                    entry = values[0];
                }
            }
            loaded = true;
        } catch (IOException | RuntimeException failure) {
            releaseLock();
            throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        }
    }

    private void persist(Entry next) {
        if (closed || lock == null || !lock.isValid())
            throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        Path temporary = null;
        try {
            if (Files.isSymbolicLink(file)) throw new IOException();
            temporary = Files.createTempFile(file.getParent(), "camera-media-pull-", ".tmp");
            byte[] bytes = json.writeValueAsBytes(next == null ? new Entry[0] : new Entry[] {next});
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
            entry = next;
        } catch (IOException failure) {
            throw new PreviewFailure("MEDIA_RECOVERY_REQUIRED");
        } finally {
            if (temporary != null)
                try {
                    Files.deleteIfExists(temporary);
                } catch (IOException ignored) {
                }
        }
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
            String api,
            String config,
            String app,
            String id,
            long notBefore,
            long checkId,
            String traceId) {
        boolean valid() {
            return api != null
                    && api.length() < 4096
                    && config != null
                    && config.length() < 4096
                    && app != null
                    && app.matches("sf_preview_[a-f0-9]{12}")
                    && id != null
                    && id.matches("[a-f0-9]{32}")
                    && notBefore >= 0
                    && checkId > 0
                    && traceId != null
                    && traceId.matches("[A-Za-z0-9_-]{1,128}");
        }
    }
}
