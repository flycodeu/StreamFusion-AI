package com.streamfusion.platform.monitor.audit.maintenance;

import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import org.springframework.stereotype.Component;

/** Local files only. No API accepts a path; published files are immutable. */
@Component
public class AuditExportStorage {
    static final long MAX_BYTES = 100L * 1024 * 1024;
    private final Path base;
    private final Path directory;
    private final String key;
    private final Set<Long> downloading = ConcurrentHashMap.newKeySet();
    private final java.util.concurrent.Semaphore downloadSlots =
            new java.util.concurrent.Semaphore(2);

    public AuditExportStorage(PlatformProperties properties) {
        base = properties.workDir();
        directory = base.resolve("exports/audit");
        key =
                UUID.nameUUIDFromBytes(
                                (base.toString()
                                                + System.getenv("COMPUTERNAME")
                                                + System.getenv("HOSTNAME"))
                                        .getBytes(StandardCharsets.UTF_8))
                        .toString()
                        .replace("-", "");
    }

    public String key() {
        return key;
    }

    Path path(long id, boolean partial) {
        return directory.resolve(id + (partial ? ".part" : ".export"));
    }

    public OutputStream create(long id) throws IOException {
        Files.createDirectories(base);
        Path exports = base.resolve("exports");
        if (Files.isSymbolicLink(exports) || Files.isSymbolicLink(directory))
            throw new IOException("Export directory is a symbolic link");
        Files.createDirectories(directory);
        if (!directory.toRealPath().startsWith(base.toRealPath()))
            throw new IOException("Invalid export directory");
        var stream =
                Files.newOutputStream(
                        path(id, true),
                        StandardOpenOption.CREATE_NEW,
                        StandardOpenOption.WRITE,
                        LinkOption.NOFOLLOW_LINKS);
        return new FilterOutputStream(stream) {
            long count;

            @Override
            public void write(int value) throws IOException {
                if (++count > MAX_BYTES) throw new IOException("Export size limit");
                out.write(value);
            }

            @Override
            public void write(byte[] bytes, int offset, int length) throws IOException {
                if ((count += length) > MAX_BYTES) throw new IOException("Export size limit");
                out.write(bytes, offset, length);
            }
        };
    }

    public long publish(long id) throws IOException {
        try {
            Files.move(path(id, true), path(id, false), StandardCopyOption.ATOMIC_MOVE);
        } catch (AtomicMoveNotSupportedException ex) {
            Files.move(path(id, true), path(id, false));
        }
        return Files.size(path(id, false));
    }

    public synchronized void remove(long id) throws IOException {
        if (downloading.contains(id)) throw new IOException("Export is being downloaded");
        Files.deleteIfExists(path(id, true));
        Files.deleteIfExists(path(id, false));
    }

    public Download open(long id) throws IOException {
        if (!downloadSlots.tryAcquire()) throw busy();
        synchronized (this) {
            if (!downloading.add(id)) {
                downloadSlots.release();
                throw busy();
            }
        }
        try {
            return new Download(
                    id,
                    Files.newInputStream(
                            path(id, false), StandardOpenOption.READ, LinkOption.NOFOLLOW_LINKS));
        } catch (IOException | RuntimeException ex) {
            downloading.remove(id);
            downloadSlots.release();
            throw ex;
        }
    }

    private static BusinessException busy() {
        return BusinessException.error(ErrorCode.RATE_LIMITED).diagnosed("EXPORT_DOWNLOAD_BUSY");
    }

    public final class Download implements AutoCloseable {
        private final long id;
        private final InputStream input;
        private boolean closed;

        private Download(long id, InputStream input) {
            this.id = id;
            this.input = input;
        }

        public void copyTo(OutputStream output) throws IOException {
            input.transferTo(output);
        }

        @Override
        public void close() throws IOException {
            if (closed) return;
            closed = true;
            try {
                input.close();
            } finally {
                downloading.remove(id);
                downloadSlots.release();
            }
        }
    }

    public boolean available(long id) {
        return Files.isRegularFile(path(id, false), LinkOption.NOFOLLOW_LINKS);
    }

    static String csv(String value) {
        String text = value == null ? "" : value;
        if (!text.isEmpty() && "=+-@\t\r\n".indexOf(text.charAt(0)) >= 0) text = "'" + text;
        return "\"" + text.replace("\"", "\"\"") + "\"";
    }
}
