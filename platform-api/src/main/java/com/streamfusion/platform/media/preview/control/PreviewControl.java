package com.streamfusion.platform.media.preview.control;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor.SourceKey;
import java.util.List;

/** Preview Control v1 is internal and independent of algorithm TaskSpec/Event/Agent Sync. */
public final class PreviewControl {
    public static final int VERSION = 1;
    public static final int MAX_BODY = 131072;

    private PreviewControl() {}

    public enum Action {
        CREATE,
        RENEW,
        ANSWER,
        STOP
    }

    public record Command(
            int protocolVersion,
            String nodeId,
            String bootId,
            String operationId,
            String traceId,
            String resourceId,
            long generation,
            String configVersion,
            long deadline,
            Action action,
            SourceKey source,
            String sourceUrl,
            List<String> videoCodecs,
            String sdp,
            Boolean transcodeAllowed) {
        public Command(
                int protocolVersion,
                String nodeId,
                String bootId,
                String operationId,
                String traceId,
                String resourceId,
                long generation,
                String configVersion,
                long deadline,
                Action action,
                SourceKey source,
                String sourceUrl,
                List<String> videoCodecs,
                String sdp) {
            this(
                    protocolVersion,
                    nodeId,
                    bootId,
                    operationId,
                    traceId,
                    resourceId,
                    generation,
                    configVersion,
                    deadline,
                    action,
                    source,
                    sourceUrl,
                    videoCodecs,
                    sdp,
                    null);
        }

        @Override
        public String toString() {
            return "PreviewCommand[action=" + action + ",operationId=" + operationId + "]";
        }
    }

    public record Receipt(String operationId, String state, String reasonCode, JsonNode result) {}

    public record Resource(
            String resourceId,
            String bootId,
            long generation,
            String state,
            long expiresAt,
            String reasonCode) {}

    public record Snapshot(
            int protocolVersion,
            String nodeId,
            String bootId,
            String version,
            String configVersion,
            String state,
            int maxSessions,
            int maxTranscodes,
            int occupiedTranscodes,
            long observedAt,
            List<Resource> resources,
            PreviewBudget budget,
            PreviewBudget.Usage usage,
            com.streamfusion.platform.media.preview.execution.PreviewAdmissionProtection.Observation
                    protection) {
        public Snapshot(
                int protocolVersion,
                String nodeId,
                String bootId,
                String version,
                String configVersion,
                String state,
                int maxSessions,
                int maxTranscodes,
                int occupiedTranscodes,
                long observedAt,
                List<Resource> resources) {
            this(
                    protocolVersion,
                    nodeId,
                    bootId,
                    version,
                    configVersion,
                    state,
                    maxSessions,
                    maxTranscodes,
                    occupiedTranscodes,
                    observedAt,
                    resources,
                    PreviewBudget.defaults(maxSessions, maxTranscodes),
                    null,
                    null);
        }
    }
}
