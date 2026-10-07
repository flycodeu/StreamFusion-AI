package com.streamfusion.platform.camera.access.pojo;

import java.util.List;
import java.util.Map;

/** Typed asset receipts inside the import pipeline; the API exposes IDs as decimal strings. */
public record CameraImportResult(
        long sourceId,
        long sourceVersion,
        List<ImportedCamera> cameras,
        int createdCount,
        int existingCount) {
    public CameraImportResult {
        cameras = List.copyOf(cameras);
    }

    public enum Status {
        CREATED,
        EXISTING
    }

    public record ImportedCamera(int candidateIndex, long cameraId, long version, Status status) {
        private Map<String, Object> toView() {
            return Map.of(
                    "candidateId", "c" + candidateIndex,
                    "cameraId", Long.toString(cameraId),
                    "version", Long.toString(version),
                    "status", status.name());
        }
    }

    public Map<String, Object> toView() {
        return Map.of(
                "sourceId", Long.toString(sourceId),
                "sourceVersion", Long.toString(sourceVersion),
                "cameras", cameras.stream().map(ImportedCamera::toView).toList(),
                "createdCount", createdCount,
                "existingCount", existingCount);
    }
}
