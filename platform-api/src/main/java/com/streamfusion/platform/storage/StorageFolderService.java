package com.streamfusion.platform.storage;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.toolkit.IdWorker;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.storage.entity.*;
import com.streamfusion.platform.storage.mapper.*;
import com.streamfusion.platform.storage.s3.*;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/**
 * Manual folder snapshots reuse the existing candidate tables; automatic cleanup stays disabled.
 */
@Service
@RequiredArgsConstructor
public class StorageFolderService {
    private static final int MAX_OBJECTS = 5000;
    private final StorageConfigurationService configuration;
    private final StorageTaskService tasks;
    private final StorageCleanupPlanMapper plans;
    private final StorageCleanupItemMapper items;
    private final StorageInstanceMapper instances;
    private final StorageScopeMapper scopes;
    private final StorageS3Client s3;
    private final StorageAccess access;
    private final StorageTokens tokens;
    private final StorageJson json;
    private final StorageViews views;

    @Transactional
    public Map<String, Object> preview(long scopeId, StorageCommands.FolderDeletion input) {
        var instance = configuration.lock(configuration.scope(scopeId).getInstanceId());
        var scope = configuration.scope(scopeId);
        access.recheck(access.actor());
        StorageRules.version(input.instanceVersion(), instance.getVersion());
        StorageRules.version(input.scopeVersion(), scope.getVersion());
        String relative = StorageRules.prefix(input.prefix());
        if (relative.isEmpty() || relative.contains("\\")) throw StorageRules.invalid();
        for (String part : relative.substring(0, relative.length() - 1).split("/", -1))
            if (part.isEmpty() || part.equals(".") || part.equals(".."))
                throw StorageRules.invalid();
        String prefix = scope.getPrefix() + relative;
        StorageRules.contains(scope.getPrefix(), prefix);
        var task =
                tasks.enqueue(
                        instance.getId(),
                        "FOLDER_PREVIEW",
                        instance.getVersion(),
                        input.idempotencyKey(),
                        access.actor(),
                        Map.of(
                                "scopeId",
                                Long.toString(scopeId),
                                "prefix",
                                prefix,
                                "scopeVersion",
                                scope.getVersion()),
                        true);
        if (plans.selectById(task.getId()) == null) {
            var plan = new StorageCleanupPlan();
            plan.setId(task.getId());
            plan.setInstanceId(instance.getId());
            plan.setScopeId(scopeId);
            plan.setActorId(access.actor());
            plan.setInstanceVersion(instance.getVersion());
            plan.setScopeVersion(scope.getVersion());
            plan.setMode("MANUAL_FOLDER");
            plan.setInputJson(json.write(Map.of("prefix", prefix)));
            plan.setStatus("SCANNING");
            plan.setCandidateCount(0L);
            plan.setCandidateBytes(0L);
            plan.setLimitReached(false);
            plan.setVersion(0L);
            plan.setCreatedAt(StorageRules.now());
            plan.setExpiresAt(StorageRules.now().plusMinutes(10));
            plans.insert(plan);
        }
        return Map.of("taskId", task.getId().toString(), "planId", task.getId().toString());
    }

    private StorageCleanupPlan require(long id) {
        var plan = plans.selectById(id);
        if (plan == null || !"MANUAL_FOLDER".equals(plan.getMode()))
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        return plan;
    }

    public Map<String, Object> detail(long id) {
        var plan = require(id);
        if (plan.getActorId() != access.actor()) throw BusinessException.error(ErrorCode.FORBIDDEN);
        var result = new LinkedHashMap<String, Object>();
        result.put("planId", Long.toString(id));
        result.put("version", plan.getVersion().toString());
        result.put("prefix", prefix(plan));
        result.put("status", plan.getStatus());
        result.put("candidateCount", plan.getCandidateCount().toString());
        result.put("candidateBytes", plan.getCandidateBytes().toString());
        result.put("expiresAt", plan.getExpiresAt());
        if (plan.getExecutionTaskId() != null)
            result.put("execution", tasks.detail(plan.getExecutionTaskId()));
        if ("READY".equals(plan.getStatus()) && fresh(plan))
            result.put(
                    "confirmationToken",
                    tokens.create(
                            "FOLDER",
                            access.actor(),
                            Map.of(
                                    "planId",
                                    Long.toString(id),
                                    "version",
                                    plan.getVersion().toString()),
                            600));
        return result;
    }

    @Transactional
    public Map<String, Object> execute(long id, StorageCommands.Execute input) {
        configuration.lock(require(id).getInstanceId());
        var plan = require(id);
        access.recheck(access.actor());
        StorageRules.version(input.planVersion(), plan.getVersion());
        if (plan.getActorId() != access.actor()) throw BusinessException.error(ErrorCode.FORBIDDEN);
        if (plan.getExecutionTaskId() != null) {
            var previous = tasks.require(plan.getExecutionTaskId());
            if (!previous.getRequestKey().equals(StorageRules.requestKey(input.idempotencyKey())))
                throw BusinessException.error(ErrorCode.CONFLICT);
            return views.task(previous);
        }
        var token = tokens.read(input.confirmationToken(), "FOLDER", access.actor());
        if (!"READY".equals(plan.getStatus())
                || !fresh(plan)
                || !token.path("planId").asText().equals(Long.toString(id))
                || !token.path("version").asText().equals(plan.getVersion().toString()))
            throw BusinessException.error(ErrorCode.STORAGE_PLAN_STALE);
        var task =
                tasks.enqueue(
                        plan.getInstanceId(),
                        "FOLDER_DELETE",
                        plan.getInstanceVersion(),
                        input.idempotencyKey(),
                        access.actor(),
                        Map.of("planId", Long.toString(id)),
                        true);
        plan.setExecutionTaskId(task.getId());
        plan.setStatus("QUEUED");
        plans.updateById(plan);
        configuration.auditConfiguration(
                "STORAGE_OBJECT", plan.getScopeId(), "FOLDER_DELETE", prefix(plan));
        return views.task(task);
    }

    private boolean fresh(StorageCleanupPlan plan) {
        return plan.getExpiresAt().isAfter(StorageRules.now())
                && configuration
                        .require(plan.getInstanceId())
                        .getVersion()
                        .equals(plan.getInstanceVersion())
                && configuration
                        .scope(plan.getScopeId())
                        .getVersion()
                        .equals(plan.getScopeVersion());
    }

    private String prefix(StorageCleanupPlan plan) {
        return json.read(plan.getInputJson()).path("prefix").asText();
    }

    public void build(StorageTask task) {
        var plan = require(task.getId());
        var instance = configuration.require(plan.getInstanceId());
        var scope = configuration.scope(plan.getScopeId());
        protection(instance, scope);
        var snapshot = snapshot(task, instance, scope, prefix(plan));
        items.delete(
                new LambdaQueryWrapper<StorageCleanupItem>()
                        .eq(StorageCleanupItem::getPlanId, plan.getId()));
        for (var object : snapshot.values()) {
            var row = new StorageCleanupItem();
            row.setId(IdWorker.getId());
            row.setPlanId(plan.getId());
            row.setObjectKey(object.key());
            row.setEtag(object.etag());
            row.setSizeBytes(object.size());
            row.setLastModified(
                    LocalDateTime.ofInstant(object.lastModified(), ZoneId.of("Asia/Shanghai")));
            row.setStatus("NOT_SENT");
            items.insert(row);
        }
        tasks.validate(task);
        plan.setCandidateCount((long) snapshot.size());
        plan.setCandidateBytes(
                snapshot.values().stream().mapToLong(StorageS3Client.ObjectInfo::size).sum());
        plan.setStatus("READY");
        plan.setVersion(plan.getVersion() + 1);
        plan.setExpiresAt(StorageRules.now().plusMinutes(10));
        plans.updateById(plan);
        tasks.finish(
                task,
                "SUCCEEDED",
                Map.of("planId", plan.getId().toString(), "objects", snapshot.size()));
    }

    private void protection(StorageInstance instance, StorageScope scope) {
        var state = s3.protection(instance, scope.getBucket());
        // Manual deletion is content-conditional. Version history and retention are outside this
        // action.
        if (state.versioning()
                || state.objectLock()
                || !"STORAGE_DELETE_IDENTITY_UNVERIFIED".equals(state.reason()))
            throw new StorageRemoteException("STORAGE_FOLDER_PROTECTION_UNSUPPORTED", 409);
    }

    private Map<String, StorageS3Client.ObjectInfo> snapshot(
            StorageTask task, StorageInstance instance, StorageScope scope, String prefix) {
        long deadline = System.nanoTime() + Duration.ofMinutes(2).toNanos();
        var result = new TreeMap<String, StorageS3Client.ObjectInfo>();
        String cursor = null;
        var seen = new HashSet<String>();
        do {
            tasks.validate(task);
            if (System.nanoTime() > deadline)
                throw new StorageRemoteException("STORAGE_FOLDER_SCAN_LIMIT", 409);
            var page = s3.list(instance, scope.getBucket(), prefix, cursor, 500, false);
            for (var object : page.items()) {
                if (object.etag() == null
                        || object.etag().isBlank()
                        || result.putIfAbsent(object.key(), object) != null)
                    throw new StorageRemoteException("STORAGE_FOLDER_CHANGED", 409);
                if (result.size() > MAX_OBJECTS)
                    throw new StorageRemoteException("STORAGE_FOLDER_SCAN_LIMIT", 409);
            }
            cursor = page.nextCursor();
            if (cursor != null && !seen.add(cursor))
                throw new StorageRemoteException("STORAGE_CURSOR_INVALID", 409);
        } while (cursor != null);
        // SeaweedFS can expose unmarked empty directories only through CommonPrefixes. A flat
        // object listing omits them, so include their current HEAD identity in this same snapshot.
        var pending = new ArrayDeque<String>();
        var directories = new HashSet<String>();
        pending.add(prefix);
        directories.add(prefix);
        while (!pending.isEmpty()) {
            String directory = pending.removeFirst();
            tasks.validate(task);
            if (System.nanoTime() > deadline)
                throw new StorageRemoteException("STORAGE_FOLDER_SCAN_LIMIT", 409);
            try {
                var marker = s3.head(instance, scope.getBucket(), directory);
                if (marker.size() != 0 || marker.etag() == null || marker.etag().isBlank())
                    throw new StorageRemoteException("STORAGE_FOLDER_CHANGED", 409);
                result.putIfAbsent(directory, marker);
                if (result.size() > MAX_OBJECTS)
                    throw new StorageRemoteException("STORAGE_FOLDER_SCAN_LIMIT", 409);
            } catch (StorageRemoteException absent) {
                // Generic S3 prefixes may have children without a HEAD-able directory object.
                if (absent.status() != 404) throw absent;
            }
            cursor = null;
            seen.clear();
            do {
                tasks.validate(task);
                if (System.nanoTime() > deadline)
                    throw new StorageRemoteException("STORAGE_FOLDER_SCAN_LIMIT", 409);
                var page = s3.list(instance, scope.getBucket(), directory, cursor, 500, true);
                for (String child : page.commonPrefixes()) {
                    if (!child.startsWith(directory)
                            || child.equals(directory)
                            || !child.endsWith("/"))
                        throw new StorageRemoteException("STORAGE_RESPONSE_INVALID", 200);
                    if (directories.add(child)) pending.addLast(child);
                    if (directories.size() > MAX_OBJECTS)
                        throw new StorageRemoteException("STORAGE_FOLDER_SCAN_LIMIT", 409);
                }
                cursor = page.nextCursor();
                if (cursor != null && !seen.add(cursor))
                    throw new StorageRemoteException("STORAGE_CURSOR_INVALID", 409);
            } while (cursor != null);
        }
        return result;
    }

    public void delete(StorageTask task) {
        var plan = require(json.read(task.getInputJson()).path("payload").path("planId").asLong());
        if (!Objects.equals(plan.getExecutionTaskId(), task.getId()) || !fresh(plan))
            throw BusinessException.error(ErrorCode.STORAGE_PLAN_STALE);
        var instance = configuration.require(plan.getInstanceId());
        var scope = configuration.scope(plan.getScopeId());
        protection(instance, scope);
        var candidates =
                items.selectList(
                        new LambdaQueryWrapper<StorageCleanupItem>()
                                .eq(StorageCleanupItem::getPlanId, plan.getId())
                                .orderByAsc(StorageCleanupItem::getId));
        var current = snapshot(task, instance, scope, prefix(plan));
        if (current.size() != candidates.size()
                || candidates.stream()
                        .anyMatch(row -> !same(row, current.get(row.getObjectKey()), true)))
            throw new StorageRemoteException("STORAGE_FOLDER_CHANGED", 409);
        // Files first, then deepest markers. New arrivals are never added to this immutable
        // selection.
        candidates.sort(
                Comparator.comparing((StorageCleanupItem row) -> row.getObjectKey().endsWith("/"))
                        .thenComparing(
                                Comparator.comparingInt(
                                                (StorageCleanupItem row) ->
                                                        row.getObjectKey().length())
                                        .reversed()));
        scopes.update(
                null,
                new LambdaUpdateWrapper<StorageScope>()
                        .eq(StorageScope::getId, scope.getId())
                        .set(StorageScope::getObservation, null));
        var usage = json.read(instance.getUsageSnapshot());
        if (usage.isObject()) {
            ((com.fasterxml.jackson.databind.node.ObjectNode) usage).put("stale", true);
            instances.update(
                    null,
                    new LambdaUpdateWrapper<StorageInstance>()
                            .eq(StorageInstance::getId, instance.getId())
                            .set(StorageInstance::getUsageSnapshot, json.write(usage)));
        }
        long deadline = System.nanoTime() + Duration.ofMinutes(10).toNanos();
        int deleted = 0;
        for (var row : candidates) {
            tasks.validate(task);
            if (System.nanoTime() > deadline)
                throw new StorageRemoteException("STORAGE_FOLDER_DELETE_LIMIT", 409);
            boolean marker = row.getObjectKey().endsWith("/");
            StorageS3Client.ObjectInfo object;
            try {
                object = s3.head(instance, scope.getBucket(), row.getObjectKey());
            } catch (StorageRemoteException ex) {
                // SeaweedFS may remove an empty parent automatically after deleting its final
                // child.
                if (marker && ex.status() == 404) {
                    row.setStatus("ABSENT");
                    items.updateById(row);
                    continue;
                }
                throw ex;
            }
            if (!same(row, object, !marker))
                throw new StorageRemoteException("STORAGE_FOLDER_CHANGED", 409);
            tasks.checkpoint(
                    task,
                    "DELETING",
                    Map.of("deleted", deleted, "total", candidates.size(), "prefix", prefix(plan)),
                    null);
            row.setStatus("SENDING");
            items.updateById(row);
            try {
                if (marker) s3.deleteEmptyMarker(instance, scope.getBucket(), object);
                else s3.deleteSelected(instance, scope.getBucket(), object);
            } catch (StorageRemoteException failure) {
                if (failure.status() != 0) {
                    row.setStatus("FAILED");
                    row.setReasonCode(failure.reason());
                    items.updateById(row);
                }
                throw failure;
            }
            row.setStatus("DELETED");
            items.updateById(row);
            deleted++;
        }
        var remaining = s3.list(instance, scope.getBucket(), prefix(plan), null, 1, false);
        var remainingDirectories =
                s3.list(instance, scope.getBucket(), prefix(plan), null, 1, true);
        boolean directoryAbsent = false;
        try {
            s3.head(instance, scope.getBucket(), prefix(plan));
        } catch (StorageRemoteException absent) {
            if (absent.status() != 404) throw absent;
            directoryAbsent = true;
        }
        String state =
                directoryAbsent
                                && remaining.items().isEmpty()
                                && !remaining.hasMore()
                                && remainingDirectories.items().isEmpty()
                                && remainingDirectories.commonPrefixes().isEmpty()
                                && !remainingDirectories.hasMore()
                        ? "SUCCEEDED"
                        : "PARTIAL";
        plan.setStatus(state);
        plans.updateById(plan);
        tasks.finish(
                task,
                state,
                Map.of(
                        "deleted",
                        deleted,
                        "total",
                        candidates.size(),
                        "prefix",
                        prefix(plan),
                        "reasonCode",
                        state.equals("SUCCEEDED")
                                ? "STORAGE_FOLDER_DELETED"
                                : "STORAGE_FOLDER_CHANGED"));
    }

    private boolean same(
            StorageCleanupItem row, StorageS3Client.ObjectInfo object, boolean modified) {
        return object != null
                && row.getEtag().equals(object.etag())
                && row.getSizeBytes() == object.size()
                && (!modified
                        || row.getLastModified()
                                .atZone(ZoneId.of("Asia/Shanghai"))
                                .toInstant()
                                .equals(object.lastModified()));
    }

    public void failed(StorageTask task, RuntimeException error) {
        long id =
                task.getType().equals("FOLDER_PREVIEW")
                        ? task.getId()
                        : json.read(task.getInputJson()).path("payload").path("planId").asLong();
        var plan = plans.selectById(id);
        if (plan == null) return;
        String reason =
                error instanceof StorageRemoteException remote
                        ? remote.reason()
                        : error instanceof BusinessException business
                                ? business.code().name()
                                : "STORAGE_FOLDER_FAILED";
        long deleted =
                items.selectCount(
                        new LambdaQueryWrapper<StorageCleanupItem>()
                                .eq(StorageCleanupItem::getPlanId, id)
                                .eq(StorageCleanupItem::getStatus, "DELETED"));
        boolean sent =
                items.selectCount(
                                new LambdaQueryWrapper<StorageCleanupItem>()
                                        .eq(StorageCleanupItem::getPlanId, id)
                                        .eq(StorageCleanupItem::getStatus, "SENDING"))
                        > 0;
        String state =
                sent
                        ? "UNKNOWN"
                        : deleted > 0
                                ? "PARTIAL"
                                : reason.equals("CANCELLED") ? "CANCELLED" : "FAILED";
        plan.setStatus(state);
        plans.updateById(plan);
        tasks.finish(
                task,
                state,
                Map.of(
                        "reasonCode",
                        reason,
                        "deleted",
                        deleted,
                        "total",
                        plan.getCandidateCount(),
                        "prefix",
                        prefix(plan),
                        "suggestion",
                        "刷新目录并查看删除记录；未处理及结果不明的项目不会自动重试"));
    }
}
