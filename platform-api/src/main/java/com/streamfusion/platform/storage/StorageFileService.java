package com.streamfusion.platform.storage;

import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.toolkit.IdWorker;
import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.storage.entity.*;
import com.streamfusion.platform.storage.mapper.*;
import com.streamfusion.platform.storage.s3.*;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.Semaphore;
import java.util.function.BiFunction;
import lombok.RequiredArgsConstructor;
import org.slf4j.MDC;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;
import org.springframework.web.multipart.MultipartFile;

/** Manual file operations: bounded uploads, durable receipts, and short transactions around I/O. */
@Service
@RequiredArgsConstructor
public class StorageFileService {
    public static final int MAX_UPLOAD_BYTES = 20 * 1024 * 1024;
    private final StorageConfigurationService configuration;
    private final StorageObjectService objects;
    private final StorageS3Client s3;
    private final StorageAccess access;
    private final StorageJson json;
    private final StorageSecrets secrets;
    private final StorageTaskMapper tasks;
    private final StorageScopeMapper scopes;
    private final StorageInstanceMapper instances;
    private final StorageViews views;
    private final PlatformTransactionManager transactions;
    private final Semaphore uploads = new Semaphore(2);

    public Map<String, Object> upload(
            long scopeId,
            Long instanceVersion,
            Long scopeVersion,
            String relativePrefix,
            String requestKey,
            MultipartFile file)
            throws IOException {
        if (file.getSize() > MAX_UPLOAD_BYTES)
            throw BusinessException.error(ErrorCode.PAYLOAD_TOO_LARGE);
        if (!uploads.tryAcquire()) throw BusinessException.error(ErrorCode.RATE_LIMITED);
        try (var input = file.getInputStream()) {
            byte[] body = input.readNBytes(MAX_UPLOAD_BYTES + 1);
            if (body.length > MAX_UPLOAD_BYTES)
                throw BusinessException.error(ErrorCode.PAYLOAD_TOO_LARGE);
            String name = segment(file.getOriginalFilename());
            String type = file.getContentType();
            if (type == null || !type.matches("[A-Za-z0-9!#$&^_.+-]+/[A-Za-z0-9!#$&^_.+-]+"))
                type = "application/octet-stream";
            String contentType = type;
            var request =
                    Map.of(
                            "scopeId",
                            scopeId,
                            "instanceVersion",
                            required(instanceVersion),
                            "scopeVersion",
                            required(scopeVersion),
                            "relativePrefix",
                            StorageRules.prefix(relativePrefix),
                            "name",
                            name,
                            "sha256",
                            S3Signature.hash(body),
                            "contentType",
                            contentType);
            return perform(
                    scopeId,
                    instanceVersion,
                    scopeVersion,
                    "FILE_UPLOAD",
                    requestKey,
                    request,
                    (instance, scope) -> {
                        String key = target(scope, relativePrefix, name);
                        s3.putIfAbsent(instance, scope.getBucket(), key, body, contentType);
                        return Map.of(
                                "objectKey",
                                key,
                                "name",
                                name,
                                "sizeBytes",
                                Integer.toString(body.length));
                    });
        } finally {
            uploads.release();
        }
    }

    public Map<String, Object> folder(long scopeId, StorageCommands.Folder input) {
        String name = segment(input.name());
        return perform(
                scopeId,
                input.instanceVersion(),
                input.scopeVersion(),
                "FOLDER_CREATE",
                input.idempotencyKey(),
                input,
                (instance, scope) -> {
                    String key = target(scope, input.relativePrefix(), name) + "/";
                    s3.putIfAbsent(
                            instance,
                            scope.getBucket(),
                            key,
                            new byte[0],
                            "application/x-directory");
                    return Map.of("objectKey", key, "name", name);
                });
    }

    public Map<String, Object> delete(long scopeId, StorageCommands.DeleteObject input) {
        mutationKey(input.key(), false);
        var request =
                Map.of(
                        "scopeId",
                        scopeId,
                        "instanceVersion",
                        required(input.instanceVersion()),
                        "scopeVersion",
                        required(input.scopeVersion()),
                        "key",
                        input.key(),
                        "selection",
                        S3Signature.hash(
                                StorageRules.text(input.objectToken(), 8192, true)
                                        .getBytes(StandardCharsets.UTF_8)));
        return perform(
                scopeId,
                input.instanceVersion(),
                input.scopeVersion(),
                "FILE_DELETE",
                input.idempotencyKey(),
                request,
                (instance, scope) -> {
                    var selected =
                            objects.selected(
                                    scope,
                                    instance,
                                    input.key(),
                                    input.objectToken(),
                                    access.actor());
                    s3.deleteSelected(instance, scope.getBucket(), selected);
                    return Map.of("objectKey", input.key(), "deleted", true);
                });
    }

    private Map<String, Object> perform(
            long scopeId,
            Long instanceVersion,
            Long scopeVersion,
            String type,
            String requestKey,
            Object request,
            BiFunction<StorageInstance, StorageScope, Map<String, Object>> action) {
        long actor = access.actor();
        String canonicalKey = StorageRules.requestKey(requestKey);
        var transaction = new TransactionTemplate(transactions);
        var task =
                Objects.requireNonNull(
                        transaction.execute(
                                status -> {
                                    var instance =
                                            configuration.lock(
                                                    configuration.scope(scopeId).getInstanceId());
                                    var scope =
                                            scopes.selectOne(
                                                    new com.baomidou.mybatisplus.core.conditions
                                                                            .query
                                                                            .LambdaQueryWrapper<
                                                                    StorageScope>()
                                                            .eq(StorageScope::getId, scopeId)
                                                            .last("FOR UPDATE"));
                                    if (scope == null)
                                        throw BusinessException.error(ErrorCode.NOT_FOUND);
                                    access.recheck(actor);
                                    var previous =
                                            configuration.savedConfiguration(
                                                    type, canonicalKey, request);
                                    if (previous != null) {
                                        if (!"SUCCEEDED".equals(previous.getStatus()))
                                            throw BusinessException.error(ErrorCode.CONFLICT)
                                                    .diagnosed(
                                                            "STORAGE_FILE_OUTCOME_PENDING_OR_FAILED");
                                        return previous;
                                    }
                                    StorageRules.version(instanceVersion, instance.getVersion());
                                    StorageRules.version(scopeVersion, scope.getVersion());
                                    configuration.busy(instance.getId());
                                    var row = new StorageTask();
                                    row.setId(IdWorker.getId());
                                    row.setRunId(IdWorker.getId());
                                    row.setInstanceId(instance.getId());
                                    row.setScopeId(scopeId);
                                    row.setActorId(actor);
                                    row.setType(type);
                                    row.setRequestKey(canonicalKey);
                                    row.setRequestFingerprint(secrets.sign(request));
                                    row.setStatus("IN_PROGRESS");
                                    row.setPhase("MANUAL_FILE_OPERATION");
                                    row.setVersion(0L);
                                    row.setTraceId(
                                            Optional.ofNullable(MDC.get("traceId"))
                                                    .orElse(
                                                            UUID.randomUUID()
                                                                    .toString()
                                                                    .replace("-", "")));
                                    row.setCancelRequested(false);
                                    row.setCreatedAt(StorageRules.now());
                                    row.setStartedAt(row.getCreatedAt());
                                    row.setLeaseUntil(row.getCreatedAt().plusMinutes(2));
                                    // Invalidate before I/O: an unknown upload outcome must block
                                    // cached-empty removal.
                                    scopes.update(
                                            null,
                                            new LambdaUpdateWrapper<StorageScope>()
                                                    .eq(StorageScope::getId, scopeId)
                                                    .set(StorageScope::getObservation, null));
                                    JsonNode snapshot = json.read(instance.getUsageSnapshot());
                                    if (snapshot.isObject() && !snapshot.isEmpty()) {
                                        ((com.fasterxml.jackson.databind.node.ObjectNode) snapshot)
                                                .put("stale", true);
                                        instances.update(
                                                null,
                                                new LambdaUpdateWrapper<StorageInstance>()
                                                        .eq(
                                                                StorageInstance::getId,
                                                                instance.getId())
                                                        .set(
                                                                StorageInstance::getUsageSnapshot,
                                                                json.write(snapshot)));
                                    }
                                    tasks.insert(row);
                                    return row;
                                }));
        if ("SUCCEEDED".equals(task.getStatus())) return views.task(task);
        try (var trace = MDC.putCloseable("taskId", task.getId().toString());
                var run = MDC.putCloseable("runId", task.getRunId().toString())) {
            access.recheck(actor);
            var instance = configuration.require(task.getInstanceId());
            var scope = configuration.scope(scopeId);
            var result = action.apply(instance, scope);
            transaction.executeWithoutResult(
                    status -> {
                        configuration.auditConfiguration(
                                "STORAGE_OBJECT",
                                scopeId,
                                type,
                                result.get("objectKey").toString());
                        finish(task, "SUCCEEDED", result);
                    });
            return views.task(task);
        } catch (RuntimeException failure) {
            String reason =
                    failure instanceof StorageRemoteException remote
                            ? remote.reason()
                            : failure instanceof BusinessException business
                                    ? business.code().name()
                                    : "STORAGE_FILE_OPERATION_FAILED";
            String state =
                    failure instanceof BusinessException
                                    || failure instanceof StorageRemoteException remote
                                            && remote.status() != 0
                            ? "FAILED"
                            : "UNKNOWN";
            transaction.executeWithoutResult(
                    status ->
                            finish(
                                    task,
                                    state,
                                    Map.of(
                                            "reasonCode",
                                            reason,
                                            "suggestion",
                                            "刷新文件列表并查看操作记录；结果不明时不会自动重试")));
            throw failure;
        }
    }

    private void finish(StorageTask task, String state, Map<String, Object> result) {
        task.setStatus(state);
        task.setPhase("FINISHED");
        task.setFinishedAt(StorageRules.now());
        task.setResultJson(json.write(result));
        task.setVersion(task.getVersion() + 1);
        tasks.updateById(task);
    }

    private static Long required(Long value) {
        if (value == null) throw StorageRules.invalid();
        return value;
    }

    private static String segment(String name) {
        name = StorageRules.text(name, 255, true);
        if (name.contains("/") || name.contains("\\") || Set.of(".", "..").contains(name))
            throw StorageRules.invalid();
        return name;
    }

    private static String target(StorageScope scope, String relative, String name) {
        String key = scope.getPrefix() + StorageRules.prefix(relative) + name;
        mutationKey(key, false);
        StorageRules.contains(scope.getPrefix(), key);
        return key;
    }

    private static void mutationKey(String key, boolean folder) {
        StorageRules.key(key, false);
        if (key.startsWith("/") || key.contains("\\") || !folder && key.endsWith("/"))
            throw StorageRules.invalid();
        for (String part : key.split("/"))
            if (part.isEmpty() || Set.of(".", "..").contains(part)) throw StorageRules.invalid();
    }
}
