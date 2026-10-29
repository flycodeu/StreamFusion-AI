package com.streamfusion.platform.storage;

import com.streamfusion.platform.storage.entity.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Explicit public projections ensure encrypted credentials never become controller responses. */
@Component
@RequiredArgsConstructor
public class StorageViews {
    private final StorageJson json;
    private final StorageSecrets secrets;

    public Map<String, Object> instance(
            StorageInstance row, List<StorageScope> scopes, Long defaultScope) {
        var view = new LinkedHashMap<String, Object>();
        view.put("instanceId", row.getId().toString());
        view.put("name", row.getName());
        view.put("endpoint", row.getEndpoint());
        view.put("region", row.getRegion());
        view.put("networkPolicyKey", row.getNetworkPolicyKey());
        view.put("metricsEndpoint", row.getMetricsEndpoint());
        view.put("managementUrl", row.getManagementUrl());
        view.put("remark", row.getRemark());
        view.put("writeEnabled", row.getWriteEnabled());
        view.put("credentialConfigured", true);
        view.put("credentialHint", "已配置");
        view.put("provider", "SEAWEEDFS");
        view.put("addressingStyle", "PATH");
        view.put("serviceState", "UNKNOWN");
        view.put("version", row.getVersion().toString());
        view.put("createdAt", row.getCreatedAt());
        view.put("updatedAt", row.getUpdatedAt());
        var observation = json.read(row.getObservation());
        boolean matching =
                observation.path("connectionFingerprint").asText().equals(secrets.fingerprint(row))
                        && observation
                                .path("instanceVersion")
                                .asText()
                                .equals(row.getVersion().toString());
        String freshness =
                observation.isEmpty()
                        ? "UNAVAILABLE"
                        : !matching
                                ? "CONFIG_CHANGED"
                                : observation.path("checkedAt").asLong() + 600
                                                < java.time.Instant.now().getEpochSecond()
                                        ? "STALE"
                                        : "FRESH";
        view.put("freshness", freshness);
        view.put(
                "accessStatus",
                matching ? observation.path("status").asText("UNCHECKED") : "UNCHECKED");
        view.put(
                "writeCheckStatus",
                scopes.stream()
                                .anyMatch(
                                        scope -> {
                                            var observed = json.read(scope.getObservation());
                                            return "PASSED"
                                                            .equals(
                                                                    observed.path(
                                                                                    "writeCheckStatus")
                                                                            .asText())
                                                    && observed.path("connectionFingerprint")
                                                            .asText()
                                                            .equals(secrets.fingerprint(row))
                                                    && observed.path("scopeVersion")
                                                            .asText()
                                                            .equals(scope.getVersion().toString())
                                                    && observed.path("checkedAt").asLong() + 600
                                                            > java.time.Instant.now()
                                                                    .getEpochSecond();
                                        })
                        ? "PASSED"
                        : "NOT_TESTED");
        view.put("observation", observation);
        view.put("checkedAt", observation.path("finishedAt"));
        view.put("usage", json.read(row.getUsageSnapshot()));
        view.put("metrics", json.read(row.getMetricsSnapshot()));
        view.put("scopes", scopes.stream().map(this::scope).toList());
        view.put(
                "isDefault",
                defaultScope != null
                        && scopes.stream().anyMatch(s -> s.getId().equals(defaultScope)));
        view.put(
                "capabilities",
                Map.of(
                        "deleteReady",
                        false,
                        "reason",
                        "STORAGE_DELETE_IDENTITY_UNVERIFIED",
                        "suggestion",
                        "可浏览、统计和预览清理候选；覆盖保护验证前禁止删除"));
        return view;
    }

    public Map<String, Object> scope(StorageScope row) {
        var view = new LinkedHashMap<String, Object>();
        view.put("scopeId", row.getId().toString());
        view.put("instanceId", row.getInstanceId().toString());
        view.put("bucket", row.getBucket());
        view.put("prefix", row.getPrefix());
        view.put("displayName", row.getDisplayName());
        view.put("probePrefix", row.getProbePrefix());
        view.put("managementMode", row.getManagementMode());
        view.put("version", row.getVersion().toString());
        view.put("observation", json.read(row.getObservation()));
        return view;
    }

    public Map<String, Object> defaultTarget(StorageDefault row) {
        var view = new LinkedHashMap<String, Object>();
        view.put("scopeId", row.getScopeId() == null ? null : row.getScopeId().toString());
        view.put("version", row.getVersion().toString());
        return view;
    }

    public Map<String, Object> task(StorageTask row) {
        var view = new LinkedHashMap<String, Object>();
        view.put("taskId", row.getId().toString());
        view.put("runId", row.getRunId().toString());
        view.put("instanceId", row.getInstanceId().toString());
        view.put("scopeId", row.getScopeId() == null ? null : row.getScopeId().toString());
        view.put("type", row.getType());
        view.put("status", row.getStatus());
        view.put("phase", row.getPhase());
        view.put("version", row.getVersion().toString());
        view.put("cancelRequested", row.getCancelRequested());
        view.put("traceId", row.getTraceId());
        view.put("acceptedAt", row.getCreatedAt());
        view.put("startedAt", row.getStartedAt());
        view.put("finishedAt", row.getFinishedAt());
        view.put("result", json.read(row.getResultJson()));
        return view;
    }
}
