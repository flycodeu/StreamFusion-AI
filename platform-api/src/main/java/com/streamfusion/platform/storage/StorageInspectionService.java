package com.streamfusion.platform.storage;

import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.storage.entity.*;
import com.streamfusion.platform.storage.mapper.*;
import com.streamfusion.platform.storage.s3.*;
import java.time.Instant;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** Read-only connection observations and resumable S3 listing scans; no binary object index. */
@Service
@RequiredArgsConstructor
public class StorageInspectionService {
    private final StorageS3Client s3;
    private final StorageHttpTransport transport;
    private final StorageConfigurationService configuration;
    private final StorageInstanceMapper instances;
    private final StorageScopeMapper scopes;
    private final StorageTaskService tasks;
    private final StorageSecrets secrets;
    private final StorageJson json;

    public Map<String, Object> checkScope(
            StorageInstance instance, StorageScope scope, String mode) {
        var steps = new ArrayList<Map<String, Object>>();
        var result = new LinkedHashMap<String, Object>();
        result.put("scopeId", scope.getId().toString());
        result.put("connectionFingerprint", secrets.fingerprint(instance));
        result.put("scopeVersion", scope.getVersion().toString());
        result.put("checkedAt", Instant.now().getEpochSecond());
        result.put("finishedAt", Instant.now().toString());
        result.put("mode", mode);
        try {
            var page = s3.list(instance, scope.getBucket(), scope.getPrefix(), null, 1, false);
            steps.add(Map.of("stage", "AUTH_LIST", "status", "PASSED", "message", "指定纳管范围可列举"));
            if (!page.items().isEmpty()) {
                s3.head(instance, scope.getBucket(), page.items().get(0).key());
                steps.add(Map.of("stage", "HEAD", "status", "PASSED", "message", "已有文件元数据可读取"));
            } else
                steps.add(
                        Map.of(
                                "stage",
                                "HEAD",
                                "status",
                                "NOT_TESTED",
                                "message",
                                "范围为空，尚无已有文件读取证据"));
            result.put("confirmedEmpty", page.items().isEmpty() && !page.hasMore());
            result.put("status", "ACCESSIBLE");
            if (!"READ_ONLY_DRAFT".equals(mode))
                result.put("protection", s3.protection(instance, scope.getBucket()));
            if ("READ_WRITE".equals(mode))
                steps.add(
                        Map.of(
                                "stage",
                                "PUT_GET_DELETE_PROBE",
                                "status",
                                "NOT_EXECUTED",
                                "errorCode",
                                "STORAGE_DELETE_IDENTITY_UNVERIFIED",
                                "message",
                                "覆盖保护未经验证，未写入探针，未执行删除"));
        } catch (StorageRemoteException ex) {
            result.put(
                    "status",
                    ex.reason().equals("STORAGE_AUTH_FAILED") ? "AUTH_FAILED" : "UNREACHABLE");
            result.put("reasonCode", ex.reason());
            result.put("confirmedEmpty", false);
            steps.add(
                    Map.of(
                            "stage",
                            "AUTH_LIST_HEAD",
                            "status",
                            "FAILED",
                            "errorCode",
                            ex.reason(),
                            "suggestion",
                            "核对网络策略、S3地址、Bucket及密钥权限"));
        }
        result.put("steps", steps);
        result.put("writeCheckStatus", "NOT_TESTED");
        return result;
    }

    public void check(StorageTask task) {
        var instance = configuration.require(task.getInstanceId());
        var payload = json.read(task.getInputJson()).path("payload");
        String mode = payload.path("mode").asText("READ_ONLY"),
                selected = payload.path("scopeId").asText();
        if (!Set.of("READ_ONLY", "READ_WRITE").contains(mode)
                || mode.equals("READ_WRITE") && selected.isEmpty()) throw StorageRules.invalid();
        var all = configuration.scopes(instance.getId());
        var selectedScopes =
                selected.isEmpty()
                        ? all
                        : all.stream().filter(s -> s.getId().toString().equals(selected)).toList();
        if (selectedScopes.isEmpty()) throw StorageRules.invalid();
        var results = new ArrayList<Map<String, Object>>();
        for (var scope : selectedScopes) {
            tasks.checkpoint(task, "CHECKING", Map.of("completed", results.size()), null);
            var observation = checkScope(instance, scope, mode);
            tasks.validate(task);
            results.add(observation);
            scopes.update(
                    null,
                    new LambdaUpdateWrapper<StorageScope>()
                            .eq(StorageScope::getId, scope.getId())
                            .eq(StorageScope::getVersion, scope.getVersion())
                            .set(StorageScope::getObservation, json.write(observation)));
        }
        var currentScopes = configuration.scopes(instance.getId());
        boolean accessible =
                currentScopes.stream()
                        .allMatch(
                                s -> {
                                    var value = json.read(s.getObservation());
                                    return value.path("status").asText().equals("ACCESSIBLE")
                                            && value.path("connectionFingerprint")
                                                    .asText()
                                                    .equals(secrets.fingerprint(instance))
                                            && value.path("scopeVersion")
                                                    .asText()
                                                    .equals(s.getVersion().toString())
                                            && value.path("checkedAt").asLong() + 600
                                                    > Instant.now().getEpochSecond();
                                });
        var summary =
                Map.of(
                        "status",
                        accessible ? "ACCESSIBLE" : "DEGRADED",
                        "instanceVersion",
                        instance.getVersion().toString(),
                        "connectionFingerprint",
                        secrets.fingerprint(instance),
                        "checkedAt",
                        Instant.now().getEpochSecond(),
                        "finishedAt",
                        Instant.now().toString(),
                        "scopes",
                        results);
        tasks.validate(task);
        instances.update(
                null,
                new LambdaUpdateWrapper<StorageInstance>()
                        .eq(StorageInstance::getId, instance.getId())
                        .eq(StorageInstance::getVersion, instance.getVersion())
                        .set(StorageInstance::getObservation, json.write(summary)));
        if (instance.getMetricsEndpoint() != null) metrics(task, instance);
        tasks.finish(task, accessible ? "SUCCEEDED" : "PARTIAL", summary);
    }

    private void metrics(StorageTask task, StorageInstance instance) {
        var result = new LinkedHashMap<String, Object>();
        result.put("observedAt", Instant.now().toString());
        result.put("source", instance.getMetricsEndpoint());
        try {
            var endpoint = java.net.URI.create(instance.getMetricsEndpoint());
            try (var response =
                    transport.exchange(
                            endpoint,
                            instance.getNetworkPolicyKey(),
                            "GET",
                            endpoint.getRawPath().isEmpty() ? "/" : endpoint.getRawPath(),
                            Map.of("host", endpoint.getRawAuthority()),
                            new byte[0],
                            false)) {
                if (response.status() != 200)
                    throw new StorageRemoteException(
                            "STORAGE_METRICS_UNAVAILABLE", response.status());
                String body =
                        new String(
                                response.bytes(2 * 1024 * 1024),
                                java.nio.charset.StandardCharsets.UTF_8);
                var samples = new ArrayList<Map<String, String>>();
                var pattern =
                        java.util.regex.Pattern.compile(
                                "^([a-zA-Z_:][a-zA-Z0-9_:]*)(\\{[^\\r\\n]{0,1000}})?\\s+(-?[0-9.eE+]+)(?:\\s+[0-9]+)?$");
                for (String line : body.split("\n")) {
                    var matcher = pattern.matcher(line.strip());
                    if (matcher.matches() && samples.size() < 100)
                        samples.add(
                                Map.of(
                                        "name",
                                        matcher.group(1),
                                        "labels",
                                        matcher.group(2) == null ? "" : matcher.group(2),
                                        "value",
                                        matcher.group(3)));
                }
                result.put("samples", samples);
                result.put("status", "AVAILABLE");
            }
        } catch (StorageRemoteException ex) {
            result.put("status", "UNAVAILABLE");
            result.put("reasonCode", ex.reason());
        }
        tasks.validate(task);
        instances.update(
                null,
                new LambdaUpdateWrapper<StorageInstance>()
                        .eq(StorageInstance::getId, instance.getId())
                        .eq(StorageInstance::getVersion, instance.getVersion())
                        .set(StorageInstance::getMetricsSnapshot, json.write(result)));
    }

    public void scan(StorageTask task) {
        var instance = configuration.require(task.getInstanceId());
        var all = configuration.scopes(instance.getId());
        var saved = json.read(task.getCursorJson());
        int index = saved.path("scopeIndex").asInt();
        String continuation = saved.path("continuation").asText();
        long count = saved.path("count").asLong(),
                bytes = saved.path("bytes").asLong(),
                probeCount = saved.path("probeCount").asLong(),
                probeBytes = saved.path("probeBytes").asLong();
        long sliceCount = 0,
                deadline = System.nanoTime() + java.util.concurrent.TimeUnit.MINUTES.toNanos(15);
        while (index < all.size()) {
            tasks.validate(task);
            var scope = all.get(index);
            var page =
                    s3.list(
                            instance,
                            scope.getBucket(),
                            scope.getPrefix(),
                            continuation.isEmpty() ? null : continuation,
                            1000,
                            false);
            for (var object : page.items()) {
                count = Math.addExact(count, 1);
                bytes = Math.addExact(bytes, object.size());
                if (scope.getProbePrefix() != null
                        && !scope.getProbePrefix().isEmpty()
                        && object.key().startsWith(scope.getProbePrefix())) {
                    probeCount++;
                    probeBytes = Math.addExact(probeBytes, object.size());
                }
            }
            sliceCount += page.items().size();
            continuation = page.nextCursor() == null ? "" : page.nextCursor();
            if (!page.hasMore()) {
                index++;
                continuation = "";
            }
            var cursor =
                    Map.of(
                            "scopeIndex",
                            index,
                            "continuation",
                            continuation,
                            "count",
                            count,
                            "bytes",
                            bytes,
                            "probeCount",
                            probeCount,
                            "probeBytes",
                            probeBytes);
            tasks.checkpoint(
                    task,
                    "LISTING",
                    Map.of(
                            "visibleObjectCount",
                            Long.toString(count - probeCount),
                            "logicalBytes",
                            Long.toString(bytes - probeBytes),
                            "complete",
                            false),
                    cursor);
            if (sliceCount >= 1_000_000 || System.nanoTime() > deadline) {
                tasks.finish(
                        task,
                        "PAUSED_LIMIT",
                        Map.of(
                                "complete",
                                false,
                                "reasonCode",
                                "STORAGE_SCAN_LIMIT",
                                "visibleObjectCount",
                                Long.toString(count - probeCount),
                                "logicalBytes",
                                Long.toString(bytes - probeBytes)));
                return;
            }
        }
        var snapshot =
                Map.of(
                        "snapshotId",
                        task.getId().toString(),
                        "instanceVersion",
                        instance.getVersion().toString(),
                        "visibleObjectCount",
                        Long.toString(count - probeCount),
                        "logicalBytes",
                        Long.toString(bytes - probeBytes),
                        "probeObjectCount",
                        Long.toString(probeCount),
                        "probeBytes",
                        Long.toString(probeBytes),
                        "startedAt",
                        task.getCreatedAt().toString(),
                        "finishedAt",
                        Instant.now().toString(),
                        "consistency",
                        "SCAN_WINDOW",
                        "complete",
                        true);
        tasks.validate(task);
        instances.update(
                null,
                new LambdaUpdateWrapper<StorageInstance>()
                        .eq(StorageInstance::getId, instance.getId())
                        .eq(StorageInstance::getVersion, instance.getVersion())
                        .set(StorageInstance::getUsageSnapshot, json.write(snapshot)));
        tasks.finish(task, "SUCCEEDED", snapshot);
    }
}
