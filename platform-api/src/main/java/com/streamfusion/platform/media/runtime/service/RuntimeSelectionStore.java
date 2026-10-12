package com.streamfusion.platform.media.runtime.service;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Selection;
import java.io.IOException;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.util.HashMap;
import java.util.Map;

/** Platform-owned selection state only. No external configuration or API secret is written. */
final class RuntimeSelectionStore {
    private final Path file;
    private final ObjectMapper mapper;

    RuntimeSelectionStore(Path workDir, ObjectMapper mapper) {
        this.file = workDir.resolve("media-runtime-selection.json");
        this.mapper = mapper;
    }

    State load() {
        if (!Files.exists(file, LinkOption.NOFOLLOW_LINKS)) return new State(0, Map.of());
        try {
            if (!Files.isRegularFile(file, LinkOption.NOFOLLOW_LINKS) || Files.size(file) > 65536)
                throw new IOException("Invalid selection file");
            State state = mapper.readValue(file.toFile(), State.class);
            if (state.revision() < 0 || state.selections() == null || state.selections().size() > 2)
                throw new IOException("Invalid selection state");
            for (var entry : state.selections().entrySet()) {
                if (!entry.getKey().equals("WINDOWS") && !entry.getKey().equals("LINUX"))
                    throw new IOException("Invalid selection OS");
                Selection value = entry.getValue();
                if (value == null
                        || !validId(value.zlmId(), "zlm")
                        || !validId(value.ffmpegId(), "ffmpeg"))
                    throw new IOException("Invalid selection ID");
            }
            return state;
        } catch (IOException | RuntimeException ignored) {
            throw BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
        }
    }

    State update(String os, Selection selection, String expectedVersion) {
        State state = load();
        if (!Long.toString(state.revision()).equals(expectedVersion))
            throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        if (state.revision() == Long.MAX_VALUE)
            throw BusinessException.error(ErrorCode.VERSION_EXHAUSTED);
        Map<String, Selection> values = new HashMap<>(state.selections());
        values.put(os, selection);
        State updated = new State(state.revision() + 1, Map.copyOf(values));
        Path temporary = null;
        try {
            Files.createDirectories(file.getParent());
            temporary = Files.createTempFile(file.getParent(), "media-runtime-selection-", ".tmp");
            mapper.writeValue(temporary.toFile(), updated);
            try {
                Files.move(
                        temporary,
                        file,
                        StandardCopyOption.REPLACE_EXISTING,
                        StandardCopyOption.ATOMIC_MOVE);
            } catch (AtomicMoveNotSupportedException ignored) {
                Files.move(temporary, file, StandardCopyOption.REPLACE_EXISTING);
            }
            return updated;
        } catch (IOException ignored) {
            throw BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
        } finally {
            if (temporary != null) {
                try {
                    Files.deleteIfExists(temporary);
                } catch (IOException ignored) {
                }
            }
        }
    }

    private static boolean validId(String value, String kind) {
        return value == null || value.matches(kind + "-[a-f0-9]{24}");
    }

    record State(long revision, Map<String, Selection> selections) {
        Selection selection(String os) {
            return selections.getOrDefault(os, new Selection(null, null));
        }
    }
}
