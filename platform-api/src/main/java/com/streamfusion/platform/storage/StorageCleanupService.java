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
 * Candidate preparation is implemented independently of destructive capability. No unsafe fallback.
 */
@Service
@RequiredArgsConstructor
public class StorageCleanupService {
    private final StorageCleanupPlanMapper plans;
    private final StorageCleanupItemMapper items;
    private final StorageCleanupRuleMapper rules;
    private final StorageConfigurationService configuration;
    private final StorageTaskService tasks;
    private final StorageObjectService objects;
    private final StorageS3Client s3;
    private final StorageAccess access;
    private final StorageTokens tokens;
    private final StorageJson json;

    @Transactional
    public Map<String, Object> preview(long scopeId, StorageCommands.Cleanup input) {
        var instance = configuration.lock(configuration.scope(scopeId).getInstanceId());
        var scope = configuration.scope(scopeId);
        StorageRules.version(input.instanceVersion(), instance.getVersion());
        StorageRules.version(input.scopeVersion(), scope.getVersion());
        access.recheck(access.actor());
        if (!Set.of("SELECTED", "FILTER", "RULE").contains(input.mode()))
            throw StorageRules.invalid();
        var payload = new LinkedHashMap<String, Object>();
        payload.put("scopeId", Long.toString(scopeId));
        payload.put("request", input);
        var task =
                tasks.enqueue(
                        instance.getId(),
                        "CLEANUP_PREVIEW",
                        instance.getVersion(),
                        input.idempotencyKey(),
                        access.actor(),
                        payload,
                        true);
        var previous = plans.selectById(task.getId());
        if (previous != null) return receipt(task, previous.getId());
        var plan = new StorageCleanupPlan();
        plan.setId(task.getId());
        plan.setInstanceId(instance.getId());
        plan.setScopeId(scopeId);
        plan.setActorId(access.actor());
        plan.setInstanceVersion(instance.getVersion());
        plan.setScopeVersion(scope.getVersion());
        plan.setMode(input.mode());
        plan.setInputJson(json.write(input));
        plan.setStatus("SCANNING");
        plan.setCandidateCount(0L);
        plan.setCandidateBytes(0L);
        plan.setLimitReached(false);
        plan.setVersion(0L);
        plan.setCreatedAt(StorageRules.now());
        plan.setExpiresAt(StorageRules.now().plusMinutes(10));
        plans.insert(plan);
        return receipt(task, plan.getId());
    }

    private Map<String, Object> receipt(StorageTask task, Long planId) {
        return Map.of(
                "taskId",
                task.getId().toString(),
                "runId",
                task.getRunId().toString(),
                "planId",
                planId.toString(),
                "status",
                task.getStatus(),
                "version",
                task.getVersion().toString());
    }

    public void build(StorageTask task) {
        var plan = require(task.getId());
        var scope = configuration.scope(plan.getScopeId());
        var instance = configuration.require(plan.getInstanceId());
        var input = json.convert(plan.getInputJson(), StorageCommands.Cleanup.class);
        items.delete(
                new LambdaQueryWrapper<StorageCleanupItem>()
                        .eq(StorageCleanupItem::getPlanId, plan.getId()));
        int maximum = input.maxDeleteObjects() == null ? 1000 : input.maxDeleteObjects();
        long budget = input.maxDeleteBytes() == null ? 10L << 30 : input.maxDeleteBytes();
        if (maximum < 1 || maximum > 5000 || budget < 1 || budget > 100L << 30)
            throw StorageRules.invalid();
        long count = 0, bytes = 0;
        boolean limited = false;
        long deadline = System.nanoTime() + java.util.concurrent.TimeUnit.MINUTES.toNanos(15);
        Instant cutoff = input.modifiedBefore();
        String prefix = scope.getPrefix() + StorageRules.prefix(input.relativePrefix());
        if (input.mode().equals("RULE")) {
            var rule = requireRule(input.ruleId());
            StorageRules.version(input.ruleVersion(), rule.getVersion());
            if (!rule.getScopeId().equals(scope.getId())) throw StorageRules.invalid();
            cutoff = Instant.now().minus(Duration.ofDays(rule.getRetentionDays()));
            prefix = scope.getPrefix() + rule.getRelativePrefix();
            maximum = rule.getMaxDeleteObjects();
            budget = rule.getMaxDeleteBytes();
            plan.setRuleId(rule.getId());
            plan.setRuleVersion(rule.getVersion());
        }
        if (input.mode().equals("SELECTED")) {
            if (input.objects() == null
                    || input.objects().isEmpty()
                    || input.objects().size() > 100) throw StorageRules.invalid();
            var seen = new HashSet<String>();
            for (var selected : input.objects()) {
                tasks.validate(task);
                if (!seen.add(selected.key())) throw StorageRules.invalid();
                var object =
                        objects.selected(
                                scope,
                                instance,
                                selected.key(),
                                selected.objectToken(),
                                task.getActorId());
                if (count >= maximum || object.size() > budget - bytes) {
                    limited = true;
                    continue;
                }
                add(plan, object);
                count++;
                bytes += object.size();
            }
        } else {
            if (cutoff == null || cutoff.isAfter(Instant.now())) throw StorageRules.invalid();
            String cursor = null;
            long scanned = 0;
            do {
                tasks.validate(task);
                var page = s3.list(instance, scope.getBucket(), prefix, cursor, 1000, false);
                scanned += page.items().size();
                for (var object : page.items()) {
                    if (!object.lastModified().isBefore(cutoff)
                            || scope.getProbePrefix() != null
                                    && !scope.getProbePrefix().isEmpty()
                                    && object.key().startsWith(scope.getProbePrefix())) continue;
                    if (count >= maximum) {
                        limited = true;
                        break;
                    }
                    if (object.size() > budget - bytes) {
                        limited = true;
                        continue;
                    }
                    add(plan, object);
                    count++;
                    bytes += object.size();
                }
                tasks.checkpoint(
                        task,
                        "PREVIEWING",
                        Map.of(
                                "candidateCount",
                                Long.toString(count),
                                "candidateBytes",
                                Long.toString(bytes)),
                        null);
                if (count >= maximum) {
                    limited = page.hasMore() || limited;
                    break;
                }
                if (System.nanoTime() > deadline || scanned >= 1_000_000)
                    throw new StorageRemoteException("STORAGE_SCAN_INCOMPLETE", 0);
                cursor = page.nextCursor();
            } while (cursor != null);
        }
        tasks.validate(task);
        plan.setCandidateCount(count);
        plan.setCandidateBytes(bytes);
        plan.setLimitReached(limited);
        plan.setStatus("READY");
        plan.setVersion(1L);
        plan.setExpiresAt(StorageRules.now().plusMinutes(10));
        plans.updateById(plan);
        tasks.finish(
                task,
                "SUCCEEDED",
                Map.of(
                        "planId",
                        plan.getId().toString(),
                        "candidateCount",
                        Long.toString(count),
                        "candidateBytes",
                        Long.toString(bytes),
                        "scanComplete",
                        true,
                        "limitReached",
                        limited,
                        "deleteReady",
                        false,
                        "reasonCode",
                        "STORAGE_DELETE_IDENTITY_UNVERIFIED"));
    }

    private void add(StorageCleanupPlan plan, StorageS3Client.ObjectInfo object) {
        var row = new StorageCleanupItem();
        row.setId(IdWorker.getId());
        row.setPlanId(plan.getId());
        row.setObjectKey(object.key());
        row.setEtag(object.etag());
        row.setSizeBytes(object.size());
        row.setLastModified(
                LocalDateTime.ofInstant(object.lastModified(), ZoneId.of("Asia/Shanghai")));
        row.setStatus("NOT_SENT");
        row.setReasonCode("STORAGE_DELETE_IDENTITY_UNVERIFIED");
        items.insert(row);
    }

    public StorageCleanupPlan require(long id) {
        var row = plans.selectById(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    public Map<String, Object> detail(long id) {
        var plan = require(id);
        var result = new LinkedHashMap<String, Object>();
        result.put("planId", plan.getId().toString());
        result.put("version", plan.getVersion().toString());
        result.put("scopeId", plan.getScopeId().toString());
        result.put("instanceId", plan.getInstanceId().toString());
        result.put("mode", plan.getMode());
        result.put("createdAt", plan.getCreatedAt());
        result.put("expiresAt", plan.getExpiresAt());
        boolean stale =
                plan.getStatus().equals("READY") && plan.getExpiresAt().isBefore(StorageRules.now())
                        || !configuration
                                .require(plan.getInstanceId())
                                .getVersion()
                                .equals(plan.getInstanceVersion())
                        || !configuration
                                .scope(plan.getScopeId())
                                .getVersion()
                                .equals(plan.getScopeVersion());
        result.put("status", stale ? "EXPIRED" : plan.getStatus());
        result.put("candidateCount", plan.getCandidateCount().toString());
        result.put("candidateBytes", plan.getCandidateBytes().toString());
        result.put("limitReached", plan.getLimitReached());
        result.put("scanComplete", plan.getStatus().equals("READY"));
        result.put("deleteReady", false);
        result.put("reasonCode", "STORAGE_DELETE_IDENTITY_UNVERIFIED");
        result.put("suggestion", "当前仅预览候选，未向存储发送删除请求");
        if (plan.getActorId() == access.actor() && !stale && plan.getStatus().equals("READY"))
            result.put(
                    "confirmationToken",
                    tokens.create(
                            "PLAN",
                            access.actor(),
                            Map.of(
                                    "planId",
                                    plan.getId().toString(),
                                    "version",
                                    plan.getVersion().toString()),
                            600));
        return result;
    }

    public Map<String, Object> items(long id, int page, int size) {
        require(id);
        if (page < 1 || size < 1 || size > 100) throw StorageRules.invalid();
        var result =
                items.selectPage(
                        new com.baomidou.mybatisplus.extension.plugins.pagination.Page<>(
                                page, size),
                        new LambdaQueryWrapper<StorageCleanupItem>()
                                .eq(StorageCleanupItem::getPlanId, id)
                                .orderByAsc(StorageCleanupItem::getId));
        return Map.of(
                "items",
                result.getRecords(),
                "page",
                page,
                "size",
                size,
                "total",
                result.getTotal());
    }

    public void execute(long id, StorageCommands.Execute input) {
        var plan = require(id);
        access.recheck(access.actor());
        StorageRules.version(input.planVersion(), plan.getVersion());
        if (plan.getActorId() != access.actor() || plan.getExpiresAt().isBefore(StorageRules.now()))
            throw BusinessException.error(ErrorCode.STORAGE_PLAN_STALE);
        var token = tokens.read(input.confirmationToken(), "PLAN", access.actor());
        if (!token.path("planId").asText().equals(Long.toString(id))
                || !token.path("version").asText().equals(plan.getVersion().toString()))
            throw BusinessException.error(ErrorCode.STORAGE_PLAN_STALE);
        throw BusinessException.error(ErrorCode.STORAGE_DELETE_UNSUPPORTED);
    }

    public List<StorageCleanupRule> rules(long scope) {
        configuration.scope(scope);
        return rules.selectList(
                new LambdaQueryWrapper<StorageCleanupRule>()
                        .eq(StorageCleanupRule::getScopeId, scope)
                        .orderByAsc(StorageCleanupRule::getId));
    }

    private StorageCleanupRule requireRule(Long id) {
        var row = id == null ? null : rules.selectById(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    @Transactional
    public StorageCleanupRule saveRule(long scopeId, Long id, StorageCommands.Rule input) {
        var scope = configuration.scope(scopeId);
        configuration.lock(scope.getInstanceId());
        access.recheck(access.actor());
        var request = Map.of("scopeId", scopeId, "input", input);
        if (id == null) {
            var previous =
                    configuration.savedConfiguration("RULE_SAVE", input.idempotencyKey(), request);
            if (previous != null)
                return json.convert(previous.getResultJson(), StorageCleanupRule.class);
        }
        if (id == null && rules(scopeId).size() >= 100)
            throw BusinessException.error(ErrorCode.RATE_LIMITED);
        var row = id == null ? new StorageCleanupRule() : requireRule(id);
        if (id != null) {
            if (!row.getScopeId().equals(scopeId))
                throw BusinessException.error(ErrorCode.FORBIDDEN);
            StorageRules.version(input.version(), row.getVersion());
        }
        if (input.retentionDays() < 1
                || input.retentionDays() > 36500
                || input.maxDeleteObjects() < 1
                || input.maxDeleteObjects() > 5000
                || input.maxDeleteBytes() < 1
                || input.maxDeleteBytes() > 100L << 30) throw StorageRules.invalid();
        if (input.scheduleTime() == null
                || !input.scheduleTime().matches("(?:[01][0-9]|2[0-3]):[0-5][0-9]"))
            throw StorageRules.invalid();
        try {
            java.time.LocalTime.parse(
                    input.scheduleTime(), java.time.format.DateTimeFormatter.ofPattern("HH:mm"));
        } catch (RuntimeException ex) {
            throw StorageRules.invalid();
        }
        row.setName(StorageRules.text(input.name(), 64, true));
        row.setRelativePrefix(StorageRules.prefix(input.relativePrefix()));
        row.setRetentionDays(input.retentionDays());
        row.setScheduleTime(input.scheduleTime());
        row.setMaxDeleteObjects(input.maxDeleteObjects());
        row.setMaxDeleteBytes(input.maxDeleteBytes());
        row.setEnabled(false);
        row.setActorId(access.actor());
        row.setScopeId(scopeId);
        row.setVersion(id == null ? 0L : row.getVersion() + 1);
        if (id == null) {
            rules.insert(row);
            configuration.recordConfiguration(
                    scope.getInstanceId(), "RULE_SAVE", input.idempotencyKey(), request, row);
        } else rules.updateById(row);
        configuration.auditConfiguration(
                "STORAGE_CLEANUP_RULE",
                row.getId(),
                id == null ? "CREATE" : "UPDATE",
                row.getName());
        return row;
    }

    @Transactional
    public StorageCleanupRule enabled(long id, StorageCommands.Enabled input) {
        var row = requireRule(id);
        configuration.lock(configuration.scope(row.getScopeId()).getInstanceId());
        access.recheck(access.actor());
        StorageRules.version(input.version(), row.getVersion());
        if (input.enabled()) throw BusinessException.error(ErrorCode.STORAGE_DELETE_UNSUPPORTED);
        row.setEnabled(false);
        row.setVersion(row.getVersion() + 1);
        rules.updateById(row);
        configuration.auditConfiguration("STORAGE_CLEANUP_RULE", id, "DISABLE", row.getName());
        return row;
    }

    @Transactional
    public void removeRule(long id, long version) {
        var row = requireRule(id);
        configuration.lock(configuration.scope(row.getScopeId()).getInstanceId());
        access.recheck(access.actor());
        StorageRules.version(version, row.getVersion());
        if (Boolean.TRUE.equals(row.getEnabled()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        configuration.auditConfiguration("STORAGE_CLEANUP_RULE", id, "REMOVE", row.getName());
        rules.deleteById(id);
    }

    public void failPreview(StorageTask task) {
        if (!task.getType().equals("CLEANUP_PREVIEW")) return;
        var candidates =
                items.selectList(
                        new LambdaQueryWrapper<StorageCleanupItem>()
                                .select(StorageCleanupItem::getSizeBytes)
                                .eq(StorageCleanupItem::getPlanId, task.getId())
                                .last("LIMIT 5000"));
        long bytes = candidates.stream().mapToLong(StorageCleanupItem::getSizeBytes).sum();
        plans.update(
                null,
                new LambdaUpdateWrapper<StorageCleanupPlan>()
                        .eq(StorageCleanupPlan::getId, task.getId())
                        .set(StorageCleanupPlan::getStatus, "INCOMPLETE")
                        .set(StorageCleanupPlan::getCandidateCount, candidates.size())
                        .set(StorageCleanupPlan::getCandidateBytes, bytes));
    }
}
