package com.streamfusion.previewnode;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewMaintenance.Command;
import com.streamfusion.platform.media.preview.control.PreviewMaintenance.State;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.util.Objects;
import java.util.Set;

/** Durable admission fence shared by Platform authorization and the fixed Ops recipe. */
final class PreviewNodeMaintenance {
    private final Path file;
    private final ObjectMapper json;
    private final String nodeId;
    private State state;

    PreviewNodeMaintenance(Path directory, ObjectMapper json, String nodeId) {
        this.file = directory.resolve("preview-maintenance.json");
        this.json = json;
        this.nodeId = nodeId;
        state = new State(1, nodeId, "OPEN", null, null, null, null, 0);
        try {
            if (Files.exists(file)) {
                if (Files.isSymbolicLink(file) || Files.size(file) > 8192)
                    throw new IllegalStateException();
                state = json.readValue(Files.readAllBytes(file), State.class);
                if (state.protocolVersion() != 1
                        || !nodeId.equals(state.nodeId())
                        || !Set.of("OPEN", "SEALED", "CLAIMED", "COMPLETE").contains(state.state())
                        || !state.state().equals("OPEN")
                                && (!id(state.permitId())
                                        || state.expiresAt() <= 0
                                        || !id(state.bootId())
                                        || state.configVersion() == null)
                        || Set.of("CLAIMED", "COMPLETE").contains(state.state())
                                && !id(state.jobId())) throw new IllegalStateException();
            }
        } catch (Exception ex) {
            throw new PreviewFailure("NODE_MAINTENANCE_DAMAGED");
        }
    }

    State state() {
        return state;
    }

    boolean sealed() {
        return !state.state().equals("OPEN");
    }

    // Caller holds the same admission lock used to reserve CREATE resources.
    State apply(Command c, String bootId, String configVersion, long now, Runnable requireEmpty) {
        if (c == null
                || c.protocolVersion() != 1
                || !nodeId.equals(c.nodeId())
                || !id(c.permitId())
                || c.deadline() <= now
                || c.deadline() > now + 20000
                || c.action() == null) throw new PreviewFailure("MAINTENANCE_COMMAND_INVALID");
        if (!Set.of("CLAIM", "FINISH").contains(c.action())
                && (!bootId.equals(c.bootId()) || !configVersion.equals(c.configVersion())))
            throw new PreviewFailure("NODE_IDENTITY_CHANGED");
        State next;
        switch (c.action()) {
            case "PREPARE" -> {
                requireEmpty.run();
                if (state.state().equals("SEALED") && state.permitId().equals(c.permitId()))
                    return state;
                if (state.state().equals("CLAIMED")
                        || state.state().equals("SEALED") && state.expiresAt() > now)
                    throw new PreviewFailure("MAINTENANCE_IN_PROGRESS");
                next =
                        new State(
                                1,
                                nodeId,
                                "SEALED",
                                c.permitId(),
                                bootId,
                                configVersion,
                                null,
                                now + 600000);
            }
            case "CLAIM" -> {
                requirePermit(c);
                if (!id(c.jobId())) throw new PreviewFailure("MAINTENANCE_COMMAND_INVALID");
                if (state.state().equals("CLAIMED") && c.jobId().equals(state.jobId()))
                    return state;
                if (!state.state().equals("SEALED")
                        || state.expiresAt() <= now
                        || !bootId.equals(state.bootId())
                        || !configVersion.equals(state.configVersion()))
                    throw new PreviewFailure("MAINTENANCE_PERMIT_STALE");
                requireEmpty.run();
                next =
                        new State(
                                1,
                                nodeId,
                                "CLAIMED",
                                state.permitId(),
                                state.bootId(),
                                state.configVersion(),
                                c.jobId(),
                                state.expiresAt());
            }
            case "FINISH" -> {
                requirePermit(c);
                if (!id(c.jobId())
                        || !c.jobId().equals(state.jobId())
                        || !Set.of("CLAIMED", "COMPLETE").contains(state.state()))
                    throw new PreviewFailure("MAINTENANCE_JOB_MISMATCH");
                requireEmpty.run();
                next =
                        new State(
                                1,
                                nodeId,
                                "COMPLETE",
                                state.permitId(),
                                state.bootId(),
                                state.configVersion(),
                                state.jobId(),
                                state.expiresAt());
            }
            case "RELEASE" -> {
                if (state.state().equals("OPEN")) return state;
                requirePermit(c);
                if (state.state().equals("CLAIMED"))
                    throw new PreviewFailure("MAINTENANCE_IN_PROGRESS");
                requireEmpty.run();
                next = new State(1, nodeId, "OPEN", null, null, null, null, 0);
            }
            default -> throw new PreviewFailure("MAINTENANCE_COMMAND_INVALID");
        }
        persist(next);
        return state;
    }

    private void requirePermit(Command c) {
        if (!Objects.equals(state.permitId(), c.permitId()))
            throw new PreviewFailure("MAINTENANCE_PERMIT_MISMATCH");
    }

    private static boolean id(String value) {
        return value != null
                && value.matches("[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}");
    }

    private void persist(State next) {
        try {
            Path temp = file.resolveSibling(file.getFileName() + ".tmp");
            try (var output =
                    FileChannel.open(
                            temp,
                            StandardOpenOption.CREATE,
                            StandardOpenOption.TRUNCATE_EXISTING,
                            StandardOpenOption.WRITE)) {
                var bytes = ByteBuffer.wrap(json.writeValueAsBytes(next));
                while (bytes.hasRemaining()) output.write(bytes);
                output.force(true);
            }
            Files.move(
                    temp,
                    file,
                    StandardCopyOption.ATOMIC_MOVE,
                    StandardCopyOption.REPLACE_EXISTING);
            state = next;
        } catch (Exception ex) {
            throw new PreviewFailure("NODE_MAINTENANCE_UNAVAILABLE");
        }
    }
}
