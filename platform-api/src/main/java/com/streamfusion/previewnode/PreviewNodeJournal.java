package com.streamfusion.previewnode;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewControl.Receipt;
import com.streamfusion.platform.media.preview.control.PreviewControl.Resource;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor.SourceKey;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.util.LinkedHashMap;
import java.util.Map;

/** Atomic, locked responsibility journal. Never persists URLs, SDP, tokens or command bodies. */
public final class PreviewNodeJournal implements AutoCloseable {
    public record Operation(String digest, long deadline, Receipt receipt) {}

    public record Viewer(
            Resource resource,
            SourceKey source,
            com.streamfusion.platform.media.preview.execution.PreviewExecutor.Playback playback,
            Boolean transcodeAllowed) {
        public Viewer(Resource resource, SourceKey source) {
            this(resource, source, null, null);
        }
    }

    public record State(
            int format, Map<String, Operation> operations, Map<String, Viewer> viewers) {}

    private final ObjectMapper json;
    private final Path file;
    private final FileChannel channel;
    private final FileLock lock;
    private State state;

    public PreviewNodeJournal(Path directory, ObjectMapper json) {
        this.json = json;
        this.file = directory.resolve("preview-node-control.json");
        FileChannel opened = null;
        FileLock acquired = null;
        try {
            Files.createDirectories(directory);
            opened =
                    FileChannel.open(
                            directory.resolve("preview-node-control.lock"),
                            StandardOpenOption.CREATE,
                            StandardOpenOption.WRITE);
            acquired = opened.tryLock();
            if (acquired == null) throw new IllegalStateException();
            state = new State(1, new LinkedHashMap<>(), new LinkedHashMap<>());
            if (Files.exists(file)) {
                if (Files.isSymbolicLink(file) || Files.size(file) > 8 * 1024 * 1024)
                    throw new IllegalStateException();
                state = json.readValue(Files.readAllBytes(file), State.class);
                if (state.format() != 1
                        || state.operations() == null
                        || state.viewers() == null
                        || state.operations().size() > 4096
                        || state.viewers().size() > 4096) throw new IllegalStateException();
            }
            channel = opened;
            lock = acquired;
        } catch (Exception ex) {
            try {
                if (acquired != null) acquired.release();
                if (opened != null) opened.close();
            } catch (Exception ignored) {
            }
            throw new PreviewFailure("NODE_JOURNAL_UNAVAILABLE");
        }
    }

    public synchronized State snapshot() {
        return new State(1, Map.copyOf(state.operations()), Map.copyOf(state.viewers()));
    }

    Path directory() {
        return file.getParent();
    }

    public synchronized Operation operation(String id) {
        return state.operations().get(id);
    }

    public synchronized Viewer viewer(String id) {
        return state.viewers().get(id);
    }

    public synchronized void save(
            String id, Operation operation, String viewerId, Viewer viewer, long now) {
        if (id != null
                && state.operations().containsKey(id)
                && !state.operations().get(id).digest().equals(operation.digest()))
            throw new PreviewFailure("OPERATION_CONFLICT");
        var operations = new LinkedHashMap<>(state.operations());
        var viewers = new LinkedHashMap<>(state.viewers());
        operations
                .entrySet()
                .removeIf(
                        e ->
                                !e.getValue().receipt().state().equals("PENDING")
                                        && e.getValue().deadline() < now - 120000);
        viewers.entrySet()
                .removeIf(
                        e ->
                                e.getValue().resource().state().equals("CLOSED")
                                        && e.getValue().resource().expiresAt() < now - 120000);
        if (id != null) operations.put(id, operation);
        if (viewerId != null) viewers.put(viewerId, viewer);
        if (operations.size() > 4096 || viewers.size() > 4096)
            throw new PreviewFailure("NODE_JOURNAL_FULL");
        var next = new State(1, operations, viewers);
        try {
            byte[] bytes = json.writeValueAsBytes(next);
            if (bytes.length > 8 * 1024 * 1024) throw new IllegalStateException();
            Path temp = file.resolveSibling(file.getFileName() + ".tmp");
            try (var output =
                    FileChannel.open(
                            temp,
                            StandardOpenOption.CREATE,
                            StandardOpenOption.TRUNCATE_EXISTING,
                            StandardOpenOption.WRITE)) {
                var buffer = java.nio.ByteBuffer.wrap(bytes);
                while (buffer.hasRemaining()) output.write(buffer);
                output.force(true);
            }
            Files.move(
                    temp,
                    file,
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING);
            state = next;
        } catch (Exception ex) {
            throw new PreviewFailure("NODE_JOURNAL_UNAVAILABLE");
        }
    }

    @Override
    public void close() {
        try {
            lock.release();
            channel.close();
        } catch (Exception ex) {
            throw new PreviewFailure("NODE_JOURNAL_UNAVAILABLE");
        }
    }
}
