package com.streamfusion.platform.storage;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.toolkit.IdWorker;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.storage.entity.*;
import com.streamfusion.platform.storage.mapper.*;
import com.streamfusion.platform.storage.s3.StorageRemoteException;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/** Durable bounded queue and ownership. Workers never keep database locks during S3 operations. */
@Service
@RequiredArgsConstructor
public class StorageTaskService {
    private final StorageTaskMapper tasks;
    private final StorageConfigurationService configuration;
    private final StorageAccess access;
    private final StorageSecrets secrets;
    private final StorageJson json;
    private final StorageViews views;

    @Transactional
    public Map<String, Object> submit(long id, String type, StorageCommands.Task input) {
        return views.task(
                enqueue(
                        id,
                        type,
                        input.version(),
                        input.idempotencyKey(),
                        access.actor(),
                        Map.of(
                                "mode",
                                input.mode() == null ? "READ_ONLY" : input.mode(),
                                "scopeId",
                                input.scopeId() == null ? "" : input.scopeId().toString()),
                        true));
    }

    @Transactional
    public StorageTask enqueue(
            long id,
            String type,
            Long version,
            String requestKey,
            long actor,
            Map<String, ?> payload,
            boolean request) {
        if (version == null) throw StorageRules.invalid();
        String key = StorageRules.requestKey(requestKey);
        String fingerprint =
                secrets.sign(
                        Map.of(
                                "instanceId",
                                id,
                                "type",
                                type,
                                "version",
                                version,
                                "payload",
                                payload));
        var previous = receipt(actor, type, key);
        if (previous != null) {
            if (!previous.getRequestFingerprint().equals(fingerprint))
                throw BusinessException.error(ErrorCode.CONFLICT);
            return previous;
        }
        var instance = configuration.lock(id);
        previous = receipt(actor, type, key);
        if (previous != null) {
            if (!previous.getRequestFingerprint().equals(fingerprint))
                throw BusinessException.error(ErrorCode.CONFLICT);
            return previous;
        }
        StorageRules.version(version, instance.getVersion());
        configuration.busy(id);
        configuration.lockRegistry();
        if (request) access.recheck(actor);
        else access.background(actor);
        if (tasks.selectCount(
                        new LambdaQueryWrapper<StorageTask>()
                                .in(
                                        StorageTask::getStatus,
                                        "QUEUED",
                                        "RUNNING",
                                        "RECONCILING",
                                        "PAUSED_LIMIT"))
                >= 20) throw BusinessException.error(ErrorCode.RATE_LIMITED);
        var row = new StorageTask();
        row.setId(IdWorker.getId());
        row.setRunId(IdWorker.getId());
        row.setInstanceId(id);
        row.setActorId(actor);
        row.setType(type);
        row.setStatus("QUEUED");
        row.setPhase("WAITING");
        row.setVersion(0L);
        row.setCancelRequested(false);
        row.setRequestKey(key);
        row.setRequestFingerprint(fingerprint);
        row.setCreatedAt(StorageRules.now());
        row.setTraceId(
                Optional.ofNullable(org.slf4j.MDC.get("traceId"))
                        .orElseGet(() -> UUID.randomUUID().toString()));
        row.setInputJson(
                json.write(
                        Map.of(
                                "instanceVersion",
                                instance.getVersion(),
                                "scopes",
                                configuration.scopes(id).stream()
                                        .map(
                                                scope ->
                                                        Map.of(
                                                                "id",
                                                                scope.getId().toString(),
                                                                "version",
                                                                scope.getVersion().toString()))
                                        .toList(),
                                "payload",
                                payload)));
        tasks.insert(row);
        return row;
    }

    private StorageTask receipt(long actor, String type, String key) {
        return tasks.selectOne(
                new LambdaQueryWrapper<StorageTask>()
                        .eq(StorageTask::getActorId, actor)
                        .eq(StorageTask::getType, type)
                        .eq(StorageTask::getRequestKey, key));
    }

    public Map<String, Object> receipt(String type, String key, long instanceId) {
        var row = receipt(access.actor(), type, StorageRules.requestKey(key));
        if (row == null || row.getInstanceId() != instanceId)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        return views.task(row);
    }

    public StorageTask require(long id) {
        var row = tasks.selectById(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    public Map<String, Object> detail(long id) {
        return views.task(require(id));
    }

    public Map<String, Object> page(long instance, int page, int size) {
        if (page < 1 || size < 1 || size > 100) throw StorageRules.invalid();
        var result =
                tasks.selectPage(
                        new com.baomidou.mybatisplus.extension.plugins.pagination.Page<>(
                                page, size),
                        new LambdaQueryWrapper<StorageTask>()
                                .eq(StorageTask::getInstanceId, instance)
                                .in(
                                        StorageTask::getType,
                                        "CHECK",
                                        "USAGE_SCAN",
                                        "CLEANUP_PREVIEW",
                                        "FILE_UPLOAD",
                                        "FILE_DELETE",
                                        "FOLDER_PREVIEW",
                                        "FOLDER_DELETE",
                                        "FOLDER_CREATE")
                                .orderByDesc(StorageTask::getCreatedAt));
        return Map.of(
                "items",
                result.getRecords().stream().map(views::task).toList(),
                "page",
                page,
                "size",
                size,
                "total",
                result.getTotal());
    }

    @Transactional
    public Map<String, Object> cancel(long id, Long version) {
        configuration.lock(require(id).getInstanceId());
        var row = require(id);
        StorageRules.version(version, row.getVersion());
        access.recheck(access.actor());
        if ("IN_PROGRESS".equals(row.getStatus()))
            throw BusinessException.error(ErrorCode.CONFLICT)
                    .diagnosed("STORAGE_FILE_OPERATION_IN_PROGRESS");
        if (Set.of("SUCCEEDED", "FAILED", "CANCELLED", "PARTIAL", "UNKNOWN")
                .contains(row.getStatus())) return views.task(row);
        String state =
                Set.of("QUEUED", "PAUSED_LIMIT").contains(row.getStatus())
                        ? "CANCELLED"
                        : row.getStatus();
        int changed =
                tasks.update(
                        null,
                        new LambdaUpdateWrapper<StorageTask>()
                                .eq(StorageTask::getId, id)
                                .eq(StorageTask::getVersion, version)
                                .set(StorageTask::getCancelRequested, true)
                                .set(StorageTask::getStatus, state)
                                .set(
                                        "CANCELLED".equals(state),
                                        StorageTask::getFinishedAt,
                                        StorageRules.now())
                                .set(StorageTask::getVersion, version + 1));
        if (changed != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        return detail(id);
    }

    @Transactional
    public Map<String, Object> resume(long id, Long version) {
        var instance = configuration.lock(require(id).getInstanceId());
        var row = require(id);
        access.recheck(access.actor());
        StorageRules.version(version, row.getVersion());
        if (!row.getType().equals("USAGE_SCAN")
                || !row.getStatus().equals("PAUSED_LIMIT")
                || row.getActorId() != access.actor()
                || !json.read(row.getInputJson())
                        .path("instanceVersion")
                        .asText()
                        .equals(instance.getVersion().toString())
                || row.getCreatedAt().isBefore(StorageRules.now().minusHours(24)))
            throw BusinessException.error(ErrorCode.CONFLICT);
        int changed =
                tasks.update(
                        null,
                        new LambdaUpdateWrapper<StorageTask>()
                                .eq(StorageTask::getId, id)
                                .eq(StorageTask::getVersion, version)
                                .set(StorageTask::getStatus, "QUEUED")
                                .set(StorageTask::getVersion, version + 1)
                                .set(StorageTask::getOwner, null)
                                .set(StorageTask::getLeaseUntil, null));
        if (changed != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        return detail(id);
    }

    public StorageTask claim(String owner) {
        var waiting =
                tasks.selectList(
                        new LambdaQueryWrapper<StorageTask>()
                                .eq(StorageTask::getStatus, "QUEUED")
                                .orderByAsc(StorageTask::getCreatedAt)
                                .last("LIMIT 4"));
        for (var row : waiting) {
            if (tasks.update(
                            null,
                            new LambdaUpdateWrapper<StorageTask>()
                                    .eq(StorageTask::getId, row.getId())
                                    .eq(StorageTask::getStatus, "QUEUED")
                                    .set(StorageTask::getStatus, "RUNNING")
                                    .set(StorageTask::getOwner, owner)
                                    .set(StorageTask::getStartedAt, StorageRules.now())
                                    .set(
                                            StorageTask::getLeaseUntil,
                                            StorageRules.now().plusMinutes(16))
                                    .setSql("version=version+1"))
                    == 1) return require(row.getId());
        }
        return null;
    }

    public void checkpoint(StorageTask task, String phase, Object result, Object cursor) {
        validate(task);
        var update =
                new LambdaUpdateWrapper<StorageTask>()
                        .eq(StorageTask::getId, task.getId())
                        .eq(StorageTask::getOwner, task.getOwner())
                        .eq(StorageTask::getStatus, "RUNNING")
                        .set(StorageTask::getPhase, phase)
                        .set(StorageTask::getLeaseUntil, StorageRules.now().plusMinutes(16))
                        .setSql("version=version+1");
        if (result != null) update.set(StorageTask::getResultJson, json.write(result));
        if (cursor != null) update.set(StorageTask::getCursorJson, json.write(cursor));
        if (tasks.update(null, update) != 1)
            throw new StorageRemoteException("STORAGE_TASK_OWNERSHIP_LOST", 0);
    }

    public void validate(StorageTask task) {
        access.background(task.getActorId());
        var current = require(task.getId());
        if (Boolean.TRUE.equals(current.getCancelRequested())
                || Thread.currentThread().isInterrupted())
            throw new StorageRemoteException("CANCELLED", 0);
        if (!"RUNNING".equals(current.getStatus())
                || !Objects.equals(current.getOwner(), task.getOwner())
                || current.getLeaseUntil().isBefore(StorageRules.now()))
            throw new StorageRemoteException("STORAGE_TASK_OWNERSHIP_LOST", 0);
        var input = json.read(task.getInputJson());
        if (!input.path("instanceVersion")
                .asText()
                .equals(configuration.require(task.getInstanceId()).getVersion().toString()))
            throw new StorageRemoteException("STORAGE_CONFIGURATION_CHANGED", 0);
        for (var scope : input.path("scopes"))
            if (!scope.path("version")
                    .asText()
                    .equals(configuration.scope(scope.path("id").asLong()).getVersion().toString()))
                throw new StorageRemoteException("STORAGE_CONFIGURATION_CHANGED", 0);
    }

    public void finish(StorageTask task, String state, Object result) {
        tasks.update(
                null,
                new LambdaUpdateWrapper<StorageTask>()
                        .eq(StorageTask::getId, task.getId())
                        .eq(StorageTask::getOwner, task.getOwner())
                        .eq(StorageTask::getStatus, "RUNNING")
                        .set(StorageTask::getStatus, state)
                        .set(StorageTask::getPhase, "FINISHED")
                        .set(StorageTask::getResultJson, json.write(result))
                        .set(StorageTask::getFinishedAt, StorageRules.now())
                        .set(StorageTask::getLeaseUntil, null)
                        .setSql("version=version+1"));
    }

    public void recover() {
        tasks.update(
                null,
                new LambdaUpdateWrapper<StorageTask>()
                        .eq(StorageTask::getStatus, "IN_PROGRESS")
                        .lt(StorageTask::getLeaseUntil, StorageRules.now())
                        .set(StorageTask::getStatus, "UNKNOWN")
                        .set(StorageTask::getPhase, "FINISHED")
                        .set(StorageTask::getFinishedAt, StorageRules.now())
                        .set(
                                StorageTask::getResultJson,
                                json.write(
                                        Map.of(
                                                "reasonCode",
                                                "STORAGE_FILE_OUTCOME_UNKNOWN",
                                                "suggestion",
                                                "手动文件操作结果不明，请刷新文件列表确认；不会自动重试"))));
        tasks.update(
                null,
                new LambdaUpdateWrapper<StorageTask>()
                        .eq(StorageTask::getStatus, "PAUSED_LIMIT")
                        .lt(StorageTask::getCreatedAt, StorageRules.now().minusHours(24))
                        .set(StorageTask::getStatus, "FAILED")
                        .set(StorageTask::getPhase, "FINISHED")
                        .set(StorageTask::getFinishedAt, StorageRules.now())
                        .set(
                                StorageTask::getResultJson,
                                json.write(
                                        Map.of(
                                                "reasonCode",
                                                "STORAGE_SCAN_INCOMPLETE",
                                                "suggestion",
                                                "扫描已超过24小时，请重新扫描"))));
        // Destructive work is never replayed after losing the worker lease.
        tasks.update(
                null,
                new LambdaUpdateWrapper<StorageTask>()
                        .eq(StorageTask::getType, "FOLDER_DELETE")
                        .eq(StorageTask::getStatus, "RUNNING")
                        .lt(StorageTask::getLeaseUntil, StorageRules.now())
                        .set(StorageTask::getStatus, "UNKNOWN")
                        .set(StorageTask::getPhase, "FINISHED")
                        .set(StorageTask::getFinishedAt, StorageRules.now())
                        .set(
                                StorageTask::getResultJson,
                                json.write(
                                        Map.of(
                                                "reasonCode", "STORAGE_FOLDER_OUTCOME_UNKNOWN",
                                                "suggestion", "刷新目录并查看删除记录；不会自动重试"))));
        // Only read-only work is safe to restart.
        tasks.update(
                null,
                new LambdaUpdateWrapper<StorageTask>()
                        .eq(StorageTask::getStatus, "RUNNING")
                        .lt(StorageTask::getLeaseUntil, StorageRules.now())
                        .set(StorageTask::getStatus, "QUEUED")
                        .set(StorageTask::getOwner, null)
                        .set(StorageTask::getLeaseUntil, null));
    }

    public List<StorageInstance> dueInstances(StorageInstanceMapper instances) {
        return instances.selectList(
                new LambdaQueryWrapper<StorageInstance>()
                        .orderByAsc(StorageInstance::getUpdatedAt)
                        .last("LIMIT 100"));
    }
}
