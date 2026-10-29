package com.streamfusion.platform.inspection.evidence;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonProperty;
import java.util.Map;

/** Snake-case machine contract matching camera-inspector UploadGrant/UploadResult. */
public final class InspectionEvidenceCommands {
    private InspectionEvidenceCommands() {}

    public record Authorize(
            @JsonProperty("request_id") String requestId,
            @JsonProperty("evidence_id") String evidenceId,
            @JsonProperty("run_id") String runId,
            @JsonProperty("assignment_epoch") Integer assignmentEpoch,
            @JsonProperty("trace_id") String traceId,
            String kind,
            @JsonProperty("size_bytes") Long sizeBytes,
            @JsonProperty("content_type") String contentType,
            @JsonProperty("checksum_base64") String checksumBase64) {}

    @JsonIgnoreProperties(ignoreUnknown = true)
    public record Complete(
            @JsonProperty("request_id") String requestId,
            @JsonProperty("evidence_id") String evidenceId,
            @JsonProperty("run_id") String runId,
            @JsonProperty("assignment_epoch") Integer assignmentEpoch,
            @JsonProperty("placement_revision") Integer placementRevision,
            @JsonProperty("trace_id") String traceId,
            String status,
            @JsonProperty("http_status") Integer httpStatus,
            @JsonProperty("bytes_sent") Long bytesSent,
            String etag,
            @JsonProperty("object_version") String objectVersion,
            @JsonProperty("reason_code") String reasonCode,
            @JsonProperty("process_id") Long processId,
            Integer returncode,
            Integer signal,
            String stage,
            @JsonProperty("stderr_tail") String stderrTail,
            @JsonProperty("cleanup_confirmed") Boolean cleanupConfirmed) {}

    public record Grant(
            @JsonProperty("evidence_id") String evidenceId,
            @JsonProperty("run_id") String runId,
            @JsonProperty("assignment_epoch") int assignmentEpoch,
            @JsonProperty("placement_revision") int placementRevision,
            @JsonProperty("trace_id") String traceId,
            @JsonProperty("instance_id") String instanceId,
            @JsonProperty("scope_id") String scopeId,
            String bucket,
            @JsonProperty("object_key") String objectKey,
            @JsonProperty("endpoint_revision") long endpointRevision,
            @JsonProperty("expires_at") java.time.Instant expiresAt,
            @JsonProperty("size_bytes") long sizeBytes,
            @JsonProperty("content_type") String contentType,
            @JsonProperty("checksum_algorithm") String checksumAlgorithm,
            @JsonProperty("checksum_base64") String checksumBase64,
            @JsonProperty("put_url") String putUrl,
            @JsonProperty("required_headers") Map<String, String> requiredHeaders) {}

    public record Completion(
            @JsonProperty("evidence_id") String evidenceId,
            @JsonProperty("placement_revision") int placementRevision,
            String state,
            @JsonProperty("reason_code") String reasonCode,
            @JsonProperty("confirmation_id") String confirmationId,
            @JsonProperty("size_bytes") Long sizeBytes,
            @JsonProperty("content_type") String contentType,
            @JsonProperty("checksum_base64") String checksumBase64,
            @JsonProperty("object_version") String objectVersion) {}
}
