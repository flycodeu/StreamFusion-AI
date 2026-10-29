package com.streamfusion.platform.storage;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.toolkit.IdWorker;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import com.streamfusion.platform.storage.entity.*;
import com.streamfusion.platform.storage.mapper.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/** Short configuration transactions only. No remote I/O while holding a row lock. */
@Service
@RequiredArgsConstructor
public class StorageConfigurationService {
    private final StorageInstanceMapper instances;
    private final StorageScopeMapper scopes;
    private final StorageDefaultMapper defaults;
    private final StorageTaskMapper tasks;
    private final StorageCleanupRuleMapper rules;
    private final StorageHistoryPolicyMapper historyPolicies;
    private final StorageSecrets secrets;
    private final StorageAccess access;
    private final StorageViews views;
    private final StorageJson json;
    private final com.streamfusion.platform.storage.s3.StorageNetwork network;
    private final AuditService audit;

    public StorageInstance require(long id) {
        var row = instances.selectById(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    public StorageInstance lock(long id) {
        var row =
                instances.selectOne(
                        new LambdaQueryWrapper<StorageInstance>()
                                .eq(StorageInstance::getId, id)
                                .last("FOR UPDATE"));
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    public StorageScope scope(long id) {
        var row = scopes.selectById(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return row;
    }

    public List<StorageScope> scopes(long id) {
        return scopes.selectList(
                new LambdaQueryWrapper<StorageScope>()
                        .eq(StorageScope::getInstanceId, id)
                        .orderByAsc(StorageScope::getId));
    }

    public StorageDefault defaultRow() {
        var row = defaults.selectById(1L);
        if (row == null) throw BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
        return row;
    }

    public void lockRegistry() {
        if (defaults.selectOne(
                        new LambdaQueryWrapper<StorageDefault>()
                                .eq(StorageDefault::getId, 1L)
                                .last("FOR UPDATE"))
                == null) throw BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
    }

    public void auditConfiguration(String targetType, Long targetId, String action, String name) {
        var attributes =
                org.springframework.web.context.request.RequestContextHolder.getRequestAttributes();
        var context =
                attributes
                                instanceof
                                org.springframework.web.context.request.ServletRequestAttributes
                                        servlet
                        ? AuditContextDto.from(servlet.getRequest())
                        : new AuditContextDto(null, null);
        audit.record(
                access.actor(),
                targetType,
                targetId,
                action,
                "SUCCESS",
                null,
                context,
                Map.of("name", name));
    }

    public Map<String, Object> detail(long id) {
        return views.instance(require(id), scopes(id), defaultRow().getScopeId());
    }

    public StorageInstance draft(Long id, StorageCommands.Instance input) {
        var row = id == null ? new StorageInstance() : require(id);
        if (id == null) row.setId(IdWorker.getId());
        else StorageRules.version(input.version(), row.getVersion());
        apply(row, input);
        return row;
    }

    public Map<String, Object> page(
            int page, int size, String keyword, Boolean writeEnabled, String accessStatus) {
        if (accessStatus != null && !accessStatus.isBlank()) throw StorageRules.invalid();
        if (page < 1 || size < 1 || size > 100 || keyword != null && keyword.length() > 100)
            throw StorageRules.invalid();
        var query = new LambdaQueryWrapper<StorageInstance>();
        if (keyword != null && !keyword.isBlank())
            query.and(
                    q ->
                            q.like(StorageInstance::getName, keyword.strip())
                                    .or()
                                    .like(StorageInstance::getEndpoint, keyword.strip()));
        if (writeEnabled != null) query.eq(StorageInstance::getWriteEnabled, writeEnabled);
        var rows =
                instances.selectPage(
                        new com.baomidou.mybatisplus.extension.plugins.pagination.Page<>(
                                page, size),
                        query.orderByDesc(StorageInstance::getCreatedAt));
        var selectedDefault = defaultRow().getScopeId();
        return Map.of(
                "items",
                rows.getRecords().stream()
                        .map(row -> views.instance(row, scopes(row.getId()), selectedDefault))
                        .toList(),
                "page",
                page,
                "size",
                size,
                "total",
                rows.getTotal());
    }

    @Transactional
    public Map<String, Object> create(StorageCommands.Instance input, AuditContextDto context) {
        long actor = access.actor();
        lockRegistry();
        String request = StorageRules.requestKey(input.idempotencyKey()),
                fingerprint = secrets.sign(input);
        var previous =
                tasks.selectOne(
                        new LambdaQueryWrapper<StorageTask>()
                                .eq(StorageTask::getActorId, actor)
                                .eq(StorageTask::getType, "REGISTER")
                                .eq(StorageTask::getRequestKey, request));
        if (previous != null) {
            if (!previous.getRequestFingerprint().equals(fingerprint))
                throw BusinessException.error(ErrorCode.CONFLICT);
            return detail(previous.getInstanceId());
        }
        if (instances.selectCount(null) >= 100)
            throw BusinessException.error(ErrorCode.RATE_LIMITED);
        var row = new StorageInstance();
        row.setId(IdWorker.getId());
        row.setVersion(0L);
        row.setWriteEnabled(false);
        row.setCreatedBy(actor);
        row.setCreatedAt(StorageRules.now());
        row.setUpdatedAt(row.getCreatedAt());
        apply(row, input);
        access.recheck(actor);
        instances.insert(row);
        if (input.scopes() == null || input.scopes().isEmpty() || input.scopes().size() > 20)
            throw StorageRules.invalid();
        for (var scope : input.scopes()) insertScope(row, scope);
        var receipt = new StorageTask();
        receipt.setId(IdWorker.getId());
        receipt.setRunId(IdWorker.getId());
        receipt.setInstanceId(row.getId());
        receipt.setActorId(actor);
        receipt.setType("REGISTER");
        receipt.setStatus("SUCCEEDED");
        receipt.setPhase("SAVED");
        receipt.setRequestKey(request);
        receipt.setRequestFingerprint(fingerprint);
        receipt.setVersion(0L);
        receipt.setCancelRequested(false);
        receipt.setCreatedAt(row.getCreatedAt());
        receipt.setFinishedAt(row.getCreatedAt());
        tasks.insert(receipt);
        record(actor, row, "CREATE", context);
        return detail(row.getId());
    }

    @Transactional
    public Map<String, Object> update(
            long id, StorageCommands.Instance input, AuditContextDto context) {
        long actor = access.actor();
        var row = lock(id);
        StorageRules.version(input.version(), row.getVersion());
        busy(id);
        access.recheck(actor);
        String endpoint = StorageRules.endpoint(input.endpoint()).toString();
        if (!row.getEndpoint().equals(endpoint) && row.getObservation() != null)
            throw BusinessException.error(ErrorCode.CONFLICT)
                    .diagnosed("STORAGE_IDENTITY_CHANGE_REJECTED");
        apply(row, input);
        row.setVersion(row.getVersion() + 1);
        row.setUpdatedAt(StorageRules.now());
        instances.updateById(row);
        record(actor, row, "UPDATE", context);
        return detail(id);
    }

    private void apply(StorageInstance row, StorageCommands.Instance input) {
        row.setName(StorageRules.text(input.name(), 64, true));
        row.setEndpoint(StorageRules.endpoint(input.endpoint()).toString());
        network.validateEndpoint(java.net.URI.create(row.getEndpoint()));
        // Keep the historical column for compatibility; it no longer selects a camera policy.
        if (row.getNetworkPolicyKey() == null) row.setNetworkPolicyKey("storage");
        row.setManagementUrl(StorageRules.managementUrl(input.managementUrl()));
        row.setRegion(
                input.region() == null ? "us-east-1" : StorageRules.text(input.region(), 64, true));
        if (!row.getRegion().matches("[A-Za-z0-9_-]{1,64}")) throw StorageRules.invalid();
        row.setRemark(StorageRules.text(input.remark(), 500, false));
        row.setMetricsEndpoint(
                input.metricsEndpoint() == null || input.metricsEndpoint().isBlank()
                        ? null
                        : StorageRules.text(input.metricsEndpoint(), 2048, true));
        if (row.getMetricsEndpoint() != null) {
            java.net.URI uri;
            try {
                uri = java.net.URI.create(row.getMetricsEndpoint());
            } catch (IllegalArgumentException ex) {
                throw StorageRules.invalid();
            }
            if (!Set.of("http", "https").contains(uri.getScheme())
                    || uri.getHost() == null
                    || uri.getRawUserInfo() != null
                    || uri.getFragment() != null
                    || uri.getQuery() != null) throw StorageRules.invalid();
            network.validateEndpoint(uri);
        }
        var old = row.getSecretCiphertext() == null ? null : secrets.read(row);
        String accessKey =
                CameraSourceRules.secret(
                        input.accessKey(),
                        old == null ? null : old.path("accessKey").asText(),
                        256,
                        false);
        String secretKey =
                CameraSourceRules.secret(
                        input.secretKey(),
                        old == null ? null : old.path("secretKey").asText(),
                        2048,
                        false);
        if (!Objects.equals(
                input.accessKey() == null ? "KEEP" : input.accessKey().action(),
                input.secretKey() == null ? "KEEP" : input.secretKey().action()))
            throw StorageRules.invalid();
        if (!accessKey.matches("[A-Za-z0-9_+=.@-]{3,256}")) throw StorageRules.invalid();
        if (old == null
                || !old.path("accessKey").asText().equals(accessKey)
                || !old.path("secretKey").asText().equals(secretKey))
            secrets.save(row, accessKey, secretKey);
    }

    private StorageScope insertScope(StorageInstance parent, StorageCommands.Scope input) {
        String bucket = StorageRules.bucket(input.bucket()),
                prefix = StorageRules.prefix(input.prefix());
        for (var existing : scopes(parent.getId()))
            if (existing.getBucket().equals(bucket)
                    && (existing.getPrefix().startsWith(prefix)
                            || prefix.startsWith(existing.getPrefix())))
                throw BusinessException.error(ErrorCode.STORAGE_SCOPE_OVERLAP);
        var row = new StorageScope();
        row.setId(IdWorker.getId());
        row.setInstanceId(parent.getId());
        row.setBucket(bucket);
        row.setPrefix(prefix);
        row.setDisplayName(StorageRules.text(input.displayName(), 64, false));
        row.setProbePrefix(StorageRules.prefix(input.probePrefix()));
        if (!row.getProbePrefix().isEmpty() && !row.getProbePrefix().startsWith(prefix))
            throw StorageRules.invalid();
        row.setIdentityDigest(
                java.util.HexFormat.of()
                        .parseHex(
                                com.streamfusion.platform.storage.s3.S3Signature.hash(
                                        (bucket + "\n" + prefix)
                                                .getBytes(
                                                        java.nio.charset.StandardCharsets.UTF_8))));
        row.setManagementMode("READ_ONLY");
        row.setVersion(0L);
        scopes.insert(row);
        return row;
    }

    @Transactional
    public Map<String, Object> addScope(
            long id, long version, StorageCommands.Scope input, String requestKey) {
        long actor = access.actor();
        var row = lock(id);
        var request = Map.of("id", id, "version", version, "scope", input);
        var previous = savedConfiguration("SCOPE_SAVE", requestKey, request);
        if (previous != null) {
            var result = new LinkedHashMap<String, Object>();
            json.read(previous.getResultJson())
                    .fields()
                    .forEachRemaining(entry -> result.put(entry.getKey(), entry.getValue()));
            return result;
        }
        StorageRules.version(version, row.getVersion());
        busy(id);
        access.recheck(actor);
        if (scopes(id).size() >= 20) throw BusinessException.error(ErrorCode.RATE_LIMITED);
        var child = insertScope(row, input);
        row.setVersion(row.getVersion() + 1);
        instances.updateById(row);
        var result =
                Map.of("scope", views.scope(child), "instanceVersion", row.getVersion().toString());
        recordConfiguration(id, "SCOPE_SAVE", requestKey, request, result);
        auditConfiguration(
                "STORAGE_SCOPE",
                child.getId(),
                "CREATE",
                child.getBucket() + "/" + child.getPrefix());
        return result;
    }

    public StorageTask savedConfiguration(String type, String requestKey, Object request) {
        var receipt =
                tasks.selectOne(
                        new LambdaQueryWrapper<StorageTask>()
                                .eq(StorageTask::getActorId, access.actor())
                                .eq(StorageTask::getType, type)
                                .eq(
                                        StorageTask::getRequestKey,
                                        StorageRules.requestKey(requestKey)));
        if (receipt != null && !receipt.getRequestFingerprint().equals(secrets.sign(request)))
            throw BusinessException.error(ErrorCode.CONFLICT);
        return receipt;
    }

    public void recordConfiguration(
            long instanceId, String type, String requestKey, Object request, Object result) {
        var row = new StorageTask();
        row.setId(IdWorker.getId());
        row.setRunId(IdWorker.getId());
        row.setInstanceId(instanceId);
        row.setActorId(access.actor());
        row.setType(type);
        row.setRequestKey(StorageRules.requestKey(requestKey));
        row.setRequestFingerprint(secrets.sign(request));
        row.setResultJson(json.write(result));
        row.setStatus("SUCCEEDED");
        row.setPhase("SAVED");
        row.setVersion(0L);
        row.setCancelRequested(false);
        row.setCreatedAt(StorageRules.now());
        row.setFinishedAt(row.getCreatedAt());
        tasks.insert(row);
    }

    @Transactional
    public Map<String, Object> editScope(long id, StorageCommands.ScopeEdit input) {
        long actor = access.actor();
        var row = lock(scope(id).getInstanceId());
        var child = scope(id);
        StorageRules.version(input.instanceVersion(), row.getVersion());
        StorageRules.version(input.version(), child.getVersion());
        busy(row.getId());
        access.recheck(actor);
        if (!"READ_ONLY".equals(input.managementMode()))
            throw BusinessException.error(ErrorCode.STORAGE_DELETE_UNSUPPORTED);
        child.setDisplayName(StorageRules.text(input.displayName(), 64, false));
        child.setProbePrefix(StorageRules.prefix(input.probePrefix()));
        if (!child.getProbePrefix().isEmpty()
                && !child.getProbePrefix().startsWith(child.getPrefix()))
            throw StorageRules.invalid();
        child.setVersion(child.getVersion() + 1);
        scopes.updateById(child);
        row.setVersion(row.getVersion() + 1);
        instances.updateById(row);
        auditConfiguration(
                "STORAGE_SCOPE",
                child.getId(),
                "UPDATE",
                child.getBucket() + "/" + child.getPrefix());
        return Map.of("scope", views.scope(child), "instanceVersion", row.getVersion().toString());
    }

    @Transactional
    public Map<String, Object> writeState(long id, StorageCommands.WriteState input) {
        long actor = access.actor();
        var row = lock(id);
        StorageRules.version(input.version(), row.getVersion());
        access.recheck(actor);
        busy(id);
        if (input.writeEnabled() && !writeProbeReady(row))
            throw BusinessException.error(ErrorCode.STORAGE_CHECK_REQUIRED)
                    .diagnosed("STORAGE_WRITE_PROBE_NOT_VERIFIED");
        var selected =
                defaults.selectOne(
                        new LambdaQueryWrapper<StorageDefault>()
                                .eq(StorageDefault::getId, 1L)
                                .last("FOR UPDATE"));
        if (!input.writeEnabled()
                && selected.getScopeId() != null
                && scopes(id).stream().anyMatch(s -> s.getId().equals(selected.getScopeId()))) {
            if (!input.clearDefault()) throw BusinessException.error(ErrorCode.CONFLICT);
            StorageRules.version(input.defaultVersion(), selected.getVersion());
            selected.setScopeId(null);
            selected.setVersion(selected.getVersion() + 1);
            defaults.updateById(selected);
        }
        auditConfiguration(
                "STORAGE_INSTANCE",
                id,
                input.writeEnabled() ? "ENABLE_WRITING" : "PAUSE_WRITING",
                row.getName());
        row.setWriteEnabled(input.writeEnabled());
        row.setVersion(row.getVersion() + 1);
        instances.updateById(row);
        return detail(id);
    }

    @Transactional
    public Map<String, Object> setDefault(StorageCommands.DefaultTarget input) {
        long actor = access.actor();
        StorageScope selectedScope = input.scopeId() == null ? null : scope(input.scopeId());
        StorageInstance selectedInstance =
                selectedScope == null ? null : lock(selectedScope.getInstanceId());
        var row =
                defaults.selectOne(
                        new LambdaQueryWrapper<StorageDefault>()
                                .eq(StorageDefault::getId, 1L)
                                .last("FOR UPDATE"));
        StorageRules.version(input.version(), row.getVersion());
        access.recheck(actor);
        if (selectedInstance != null) busy(selectedInstance.getId());
        if (input.scopeId() != null) {
            if (input.instanceVersion() == null || input.scopeVersion() == null)
                throw BusinessException.error(ErrorCode.PRECONDITION_REQUIRED);
            selectedScope = scope(input.scopeId());
            StorageRules.version(input.instanceVersion(), selectedInstance.getVersion());
            StorageRules.version(input.scopeVersion(), selectedScope.getVersion());
            if (!Boolean.TRUE.equals(selectedInstance.getWriteEnabled())
                    || !writeProbeReady(selectedInstance, selectedScope))
                throw BusinessException.error(ErrorCode.STORAGE_CHECK_REQUIRED)
                        .diagnosed("STORAGE_WRITE_PROBE_NOT_VERIFIED");
            row.setScopeId(selectedScope.getId());
        } else row.setScopeId(null);
        row.setVersion(row.getVersion() + 1);
        defaults.updateById(row);
        auditConfiguration(
                "STORAGE_DEFAULT", 1L, input.scopeId() == null ? "CLEAR" : "SET", "全局默认位置");
        return views.defaultTarget(row);
    }

    private boolean writeProbeReady(StorageInstance instance) {
        return scopes(instance.getId()).stream()
                .anyMatch(scope -> writeProbeReady(instance, scope));
    }

    private boolean writeProbeReady(StorageInstance instance, StorageScope scope) {
        var observation = json.read(scope.getObservation());
        return "ACCESSIBLE".equals(observation.path("status").asText())
                && "PASSED".equals(observation.path("writeCheckStatus").asText())
                && secrets.fingerprint(instance)
                        .equals(observation.path("connectionFingerprint").asText())
                && scope.getVersion().toString().equals(observation.path("scopeVersion").asText())
                && observation.path("checkedAt").asLong() + 600
                        > java.time.Instant.now().getEpochSecond();
    }

    public void busy(long id) {
        if (tasks.selectCount(
                        new LambdaQueryWrapper<StorageTask>()
                                .eq(StorageTask::getInstanceId, id)
                                .in(
                                        StorageTask::getStatus,
                                        "QUEUED",
                                        "RUNNING",
                                        "RECONCILING",
                                        "IN_PROGRESS",
                                        "PAUSED_LIMIT"))
                > 0) throw BusinessException.error(ErrorCode.STORAGE_TASK_BUSY);
    }

    @Transactional
    public void removeScope(long id, long version) {
        long actor = access.actor();
        var parent = lock(scope(id).getInstanceId());
        var child = scope(id);
        StorageRules.version(version, child.getVersion());
        busy(parent.getId());
        access.recheck(actor);
        var observation = json.read(child.getObservation());
        boolean freshEmpty =
                observation.path("confirmedEmpty").asBoolean()
                        && observation
                                .path("connectionFingerprint")
                                .asText()
                                .equals(secrets.fingerprint(parent))
                        && observation
                                .path("scopeVersion")
                                .asText()
                                .equals(child.getVersion().toString())
                        && observation.path("checkedAt").asLong() + 600
                                > java.time.Instant.now().getEpochSecond();
        if (Boolean.TRUE.equals(parent.getWriteEnabled())
                || id == Optional.ofNullable(defaultRow().getScopeId()).orElse(-1L)
                || rules.selectCount(
                                new LambdaQueryWrapper<StorageCleanupRule>()
                                        .eq(StorageCleanupRule::getScopeId, id))
                        > 0
                || !freshEmpty) throw BusinessException.error(ErrorCode.CONFLICT);
        auditConfiguration(
                "STORAGE_SCOPE", id, "REMOVE", child.getBucket() + "/" + child.getPrefix());
        scopes.deleteById(id);
        parent.setVersion(parent.getVersion() + 1);
        instances.updateById(parent);
    }

    @Transactional
    public void remove(long id, long version, AuditContextDto context) {
        long actor = access.actor();
        var row = lock(id);
        StorageRules.version(version, row.getVersion());
        busy(id);
        access.recheck(actor);
        if (Boolean.TRUE.equals(row.getWriteEnabled()) || !scopes(id).isEmpty())
            throw BusinessException.error(ErrorCode.CONFLICT);
        record(actor, row, "REMOVE", context);
        historyPolicies.deleteById(id);
        instances.deleteById(id);
    }

    private void record(long actor, StorageInstance row, String action, AuditContextDto context) {
        audit.record(
                actor,
                "STORAGE_INSTANCE",
                row.getId(),
                action,
                "SUCCESS",
                null,
                context,
                Map.of("name", row.getName()));
    }
}
