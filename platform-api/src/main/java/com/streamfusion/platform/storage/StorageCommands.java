package com.streamfusion.platform.storage;

import com.streamfusion.platform.camera.pojo.dto.SecretWrite;
import java.util.List;

/** Request values only; persistent records and secret-bearing transport snapshots stay separate. */
public final class StorageCommands {
    private StorageCommands() {}

    public record Scope(String bucket, String prefix, String displayName, String probePrefix) {}

    public record Instance(
            String name,
            String endpoint,
            String region,
            String networkPolicyKey,
            String metricsEndpoint,
            String managementUrl,
            String remark,
            SecretWrite accessKey,
            SecretWrite secretKey,
            List<Scope> scopes,
            Long version,
            String idempotencyKey) {
        @Override
        public String toString() {
            return "StorageInstanceCommand[redacted]";
        }
    }

    public record WriteState(
            Long version, boolean writeEnabled, boolean clearDefault, Long defaultVersion) {}

    public record DefaultTarget(
            Long version, Long scopeId, Long instanceVersion, Long scopeVersion) {}

    public record ScopeEdit(
            Long version,
            Long instanceVersion,
            String displayName,
            String probePrefix,
            String managementMode) {}

    public record Task(Long version, Long scopeId, String mode, String idempotencyKey) {}

    public record ObjectSelection(String key, String objectToken) {}

    public record Folder(
            Long instanceVersion,
            Long scopeVersion,
            String relativePrefix,
            String name,
            String idempotencyKey) {}

    public record DeleteObject(
            Long instanceVersion,
            Long scopeVersion,
            String key,
            String objectToken,
            String idempotencyKey) {
        @Override
        public String toString() {
            return "StorageDeleteObject[redacted]";
        }
    }

    public record Cleanup(
            Long instanceVersion,
            Long scopeVersion,
            String mode,
            List<ObjectSelection> objects,
            String relativePrefix,
            java.time.Instant modifiedBefore,
            Integer maxDeleteObjects,
            Long maxDeleteBytes,
            Long ruleId,
            Long ruleVersion,
            String idempotencyKey) {}

    public record Execute(Long planVersion, String confirmationToken, String idempotencyKey) {}

    public record FolderDeletion(
            Long instanceVersion, Long scopeVersion, String prefix, String idempotencyKey) {}

    public record Rule(
            String name,
            String relativePrefix,
            int retentionDays,
            String scheduleTime,
            int maxDeleteObjects,
            long maxDeleteBytes,
            Long version,
            String idempotencyKey) {}

    public record Enabled(Long version, boolean enabled, Long validationPlanId) {}
}
