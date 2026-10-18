package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.media.preview.PreviewFailure;
import java.time.Instant;

/** Stable deployment provenance; addresses and display names are never identities. */
public record OpsNodeSource(
        String controllerId,
        String targetId,
        String instanceId,
        String releaseId,
        Instant syncedAt) {
    public OpsNodeSource {
        if (!valid(controllerId) || !valid(targetId) || !valid(instanceId) || !valid(releaseId))
            throw new PreviewFailure("OPS_RESPONSE_INVALID");
    }

    public static OpsNodeSource optional(
            String controller, String target, String instance, String release) {
        if (controller == null && target == null) return null;
        return new OpsNodeSource(controller, target, instance, release, null);
    }

    public boolean sameInstance(OpsNodeSource other) {
        return other != null
                && controllerId.equals(other.controllerId)
                && targetId.equals(other.targetId)
                && instanceId.equals(other.instanceId);
    }

    private static boolean valid(String value) {
        return value != null && value.matches("[A-Za-z0-9_-]{1,64}");
    }
}
