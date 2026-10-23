package com.streamfusion.platform.monitor.security;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.*;
import java.util.List;

/** One bounded atomic snapshot per work directory. Never overwrite unreadable evidence. */
final class SecurityAlertStore implements AutoCloseable {
    private static final int MAX_BYTES = 2 * 1024 * 1024;
    private final Path directory;
    private final ObjectMapper json;
    private FileChannel channel;
    private FileLock lock;
    private boolean writable;
    private volatile boolean healthy = true;

    SecurityAlertStore(Path workDir, ObjectMapper json) {
        this.directory = workDir.resolve("security");
        this.json = json;
    }

    List<SecurityAlert> open() {
        try {
            // Refuse symlink ancestors, including the configured work directory.
            for (Path p = directory; p != null; p = p.getParent())
                if (Files.isSymbolicLink(p)) throw new IOException("symbolic link");
            Files.createDirectories(directory);
            channel =
                    FileChannel.open(
                            directory.resolve("alerts.lock"),
                            StandardOpenOption.CREATE,
                            StandardOpenOption.WRITE,
                            LinkOption.NOFOLLOW_LINKS);
            lock = channel.tryLock();
            if (lock == null) throw new IOException("work directory in use");
            Path file = directory.resolve("alerts.json");
            List<SecurityAlert> loaded = List.of();
            if (Files.exists(file, LinkOption.NOFOLLOW_LINKS)) {
                try (var input = Files.newInputStream(file, LinkOption.NOFOLLOW_LINKS)) {
                    byte[] bytes = input.readNBytes(MAX_BYTES + 1);
                    if (bytes.length > MAX_BYTES) throw new IOException("snapshot limit");
                    loaded =
                            json.readValue(
                                    bytes,
                                    json.getTypeFactory()
                                            .constructCollectionType(
                                                    List.class, SecurityAlert.class));
                }
                if (loaded.size() > 1000
                        || loaded.stream().anyMatch(java.util.Objects::isNull)
                        || loaded.stream().map(SecurityAlert::id).distinct().count()
                                != loaded.size()) throw new IOException("invalid snapshot");
            }
            writable = true;
            return List.copyOf(loaded);
        } catch (IOException | RuntimeException failure) {
            healthy = false;
            return List.of();
        }
    }

    boolean save(List<SecurityAlert> alerts) {
        if (!writable) return false;
        Path temp = null;
        try {
            byte[] bytes = json.writeValueAsBytes(alerts);
            if (bytes.length > MAX_BYTES) throw new IOException("snapshot limit");
            // A fixed staging name keeps failed cleanup/crash residue bounded to one file.
            temp = directory.resolve("alerts.pending");
            try (var out =
                    FileChannel.open(
                            temp,
                            StandardOpenOption.CREATE,
                            StandardOpenOption.TRUNCATE_EXISTING,
                            StandardOpenOption.WRITE,
                            LinkOption.NOFOLLOW_LINKS)) {
                java.nio.ByteBuffer buffer = java.nio.ByteBuffer.wrap(bytes);
                while (buffer.hasRemaining()) out.write(buffer);
                out.force(true);
            }
            Files.move(
                    temp,
                    directory.resolve("alerts.json"),
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING);
            healthy = true;
            return true;
        } catch (IOException | RuntimeException failure) {
            healthy = false;
            return false;
        } finally {
            if (temp != null)
                try {
                    Files.deleteIfExists(temp);
                } catch (IOException ignored) {
                    /* Reuse this single staging file on the next flush. */
                }
        }
    }

    boolean healthy() {
        return healthy;
    }

    @Override
    public void close() {
        try {
            if (lock != null) lock.release();
        } catch (IOException ignored) {
            healthy = false;
        }
        try {
            if (channel != null) channel.close();
        } catch (IOException ignored) {
            healthy = false;
        }
    }
}
