package com.streamfusion.platform.camera.access.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.incrementer.IdentifierGenerator;
import com.fasterxml.jackson.core.type.TypeReference;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.pojo.*;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.mapper.CameraAccessJobMapper;
import com.streamfusion.platform.camera.mapper.CameraSourceMapper;
import com.streamfusion.platform.camera.service.*;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.security.MessageDigest;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.slf4j.MDC;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

/**
 * Short transactions own durable tickets. Discovery networking is performed outside this service.
 */
@Service
@RequiredArgsConstructor
public class CameraAccessJobsService {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private static final Set<String> ACTIVE = Set.of("QUEUED", "RUNNING");
    private final CameraAccessJobMapper jobs;
    private final CameraSourceMapper sources;
    private final AccessMapper coordination;
    private final CameraAccessService access;
    private final CameraCryptoService crypto;
    private final CameraProperties properties;
    private final CameraConnectionRules connections;
    private final CameraAccessAdapterRegistry adapters;
    private final CameraNetworkScan scans;
    private final CameraAccessImportService imports;
    private final CameraJobActor actors;
    private final ObjectMapper json;
    private final IdentifierGenerator ids;
    private final Clock clock;

    public record Payload(
            String sessionId,
            CameraConnection connection,
            CameraAccessCatalog catalog,
            CameraNetworkScan.Plan scan,
            CameraNetworkScan.Result scanResult,
            BulkControl bulk) {
        public Payload(
                String sessionId,
                CameraConnection connection,
                CameraAccessCatalog catalog,
                CameraNetworkScan.Plan scan,
                CameraNetworkScan.Result scanResult) {
            this(sessionId, connection, catalog, scan, scanResult, null);
        }

        public Payload(String sessionId, CameraConnection connection, CameraAccessCatalog catalog) {
            this(sessionId, connection, catalog, null, null);
        }

        @Override
        public String toString() {
            return "Payload[redacted]";
        }
    }

    public record BulkControl(
            Long groupId, String scopeFingerprint, String sourceIp, String clientSummary) {}

    public record Work(long id, long version, long userId, String traceId, Payload payload) {
        @Override
        public String toString() {
            return "Work[id=" + id + "]";
        }
    }

    public Map<String, Object> options() {
        access.requireSuper(access.readActor());
        boolean ready = crypto.ready() && !properties.getNetworkPolicies().isEmpty();
        return Map.of(
                "ready",
                ready,
                "methods",
                adapters.methods(),
                "adapters",
                adapters.descriptors(),
                "networkPolicies",
                properties.getNetworkPolicies().entrySet().stream()
                        .map(
                                e ->
                                        Map.of(
                                                "key",
                                                e.getKey(),
                                                "name",
                                                e.getValue().getName() == null
                                                        ? e.getKey()
                                                        : e.getValue().getName()))
                        .toList(),
                "diagnostics",
                ready ? List.of() : List.of("ACCESS_SERVICE_NOT_CONFIGURED"));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> create(String requestId, CameraConnection input, String sessionId) {
        return createInternal(requestId, input, sessionId, false);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> createScan(CameraScanRequest input, String sessionId) {
        if (input == null) throw CameraSourceRules.invalid();
        return createInternal(input.clientRequestId(), input, sessionId, true);
    }

    private Map<String, Object> createInternal(
            String requestId, Object input, String sessionId, boolean scanning) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        requestTime(requestId, false);
        var existing =
                jobs.selectList(
                        new LambdaQueryWrapper<CameraAccessJobEntity>()
                                .eq(CameraAccessJobEntity::getActorUserId, actor.userId())
                                .eq(CameraAccessJobEntity::getClientRequestId, requestId));
        for (var job : existing) {
            if (!sameSession(job, sessionId)) continue;
            if (!MessageDigest.isEqual(
                    job.getRequestFingerprint(),
                    crypto.digest(job.getDigestKeyId(), "access-request", input)))
                throw error(ErrorCode.CONFLICT);
            expire(job);
            if ("EXPIRED".equals(job.getStatus())) throw error(ErrorCode.CONFLICT);
            return view(job);
        }
        requestTime(requestId, true);
        long recent =
                jobs.selectCount(
                        new LambdaQueryWrapper<CameraAccessJobEntity>()
                                .eq(CameraAccessJobEntity::getActorUserId, actor.userId())
                                .ge(CameraAccessJobEntity::getCreatedAt, now().minusMinutes(1)));
        long pending =
                jobs.selectCount(
                        new LambdaQueryWrapper<CameraAccessJobEntity>()
                                .eq(CameraAccessJobEntity::getActorUserId, actor.userId())
                                .in(CameraAccessJobEntity::getStatus, ACTIVE)
                                .gt(CameraAccessJobEntity::getExpiresAt, now()));
        long all =
                jobs.selectCount(
                        new LambdaQueryWrapper<CameraAccessJobEntity>()
                                .in(CameraAccessJobEntity::getStatus, ACTIVE)
                                .gt(CameraAccessJobEntity::getExpiresAt, now()));
        long globalRecent =
                jobs.selectCount(
                        new LambdaQueryWrapper<CameraAccessJobEntity>()
                                .ge(CameraAccessJobEntity::getCreatedAt, now().minusMinutes(1)));
        if (recent >= 10 || pending >= 4 || all >= 20 || globalRecent >= 20)
            throw error(ErrorCode.RATE_LIMITED);
        CameraConnection connection =
                scanning
                        ? null
                        : connections.normalize(
                                imports.resolveConnection((CameraConnection) input));
        CameraNetworkScan.Plan scan = scanning ? scans.prepare((CameraScanRequest) input) : null;
        var job = new CameraAccessJobEntity();
        job.setId(ids.nextId(job).longValue());
        job.setActorUserId(actor.userId());
        job.setDigestKeyId(crypto.activeKeyId());
        job.setActorSessionDigest(crypto.digest(job.getDigestKeyId(), "session", sessionId));
        job.setClientRequestId(requestId);
        job.setRequestFingerprint(crypto.digest(job.getDigestKeyId(), "access-request", input));
        job.setStatus("QUEUED");
        job.setMethod(scanning ? "SCAN" : connection.method());
        job.setJobKind(scanning ? "SCAN" : "CATALOG");
        job.setVersion(0L);
        job.setSourceId(
                connection == null || connection.sourceId() == null
                        ? null
                        : DecimalInput.id(connection.sourceId(), "sourceId"));
        job.setOriginTraceId(MDC.get("traceId"));
        job.setCreatedAt(now());
        job.setUpdatedAt(now());
        job.setExpiresAt(now().plusMinutes(15));
        encrypt(job, new Payload(sessionId, connection, null, scan, null));
        jobs.insert(job);
        return view(job);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> read(String id, String sessionId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var job = owned(id, sessionId, actor.userId());
        expire(job);
        return view(job);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> cancel(String id, String sessionId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var job = owned(id, sessionId, actor.userId());
        expire(job);
        if (!("BULK_IMPORT".equals(job.getJobKind()) && !ACTIVE.contains(job.getStatus()))
                && !Set.of("CANCELLED", "EXPIRED").contains(job.getStatus())
                && job.getResultSummary() == null) {
            job.setStatus("CANCELLED");
            job.setReasonCode("CANCELLED_BY_USER");
            advance(job);
            clearSecret(job);
        }
        return view(job);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> preview(
            String id, String version, ImportSelection selection, String sessionId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var job = ready(id, version, sessionId, actor.userId());
        var payload = payload(job);
        checkImportSource(payload.connection());
        return imports.preview(
                job.getId(),
                job.getVersion(),
                payload.connection(),
                payload.catalog(),
                selection,
                actor);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> importSelected(
            String id,
            String version,
            ImportSelection selection,
            String requestId,
            String sessionId,
            AuditContextDto audit) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var job = owned(id, sessionId, actor.userId());
        expire(job);
        requestTime(requestId, false);
        var command =
                Map.of(
                        "version",
                        version,
                        "selections",
                        new ImportSelection(
                                selection.selections(),
                                selection.groupId(),
                                null,
                                selection.targetCameraId(),
                                selection.targetCameraVersion()));
        byte[] fingerprint = crypto.digest(job.getDigestKeyId(), "access-import", command);
        if (job.getResultSummary() != null && !"EXPIRED".equals(job.getStatus())) {
            if (!requestId.equals(job.getImportRequestId())
                    || !MessageDigest.isEqual(fingerprint, job.getImportFingerprint()))
                throw error(ErrorCode.CONFLICT);
            return result(job);
        }
        requestTime(requestId, true);
        requireReady(job, version);
        var payload = payload(job);
        checkImportSource(payload.connection());
        var result =
                imports.execute(
                        job.getId(),
                        job.getVersion(),
                        payload.connection(),
                        payload.catalog(),
                        selection,
                        actor,
                        audit);
        String safe = crypto.canonical(result);
        if (safe.length() > 131072) throw error(ErrorCode.INTERNAL_ERROR);
        job.setResultSummary(safe);
        job.setImportRequestId(requestId);
        job.setImportFingerprint(fingerprint);
        job.setSourceId(Long.parseLong(result.get("sourceId").toString()));
        advance(job);
        return result;
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Work claim(long id) {
        coordination.lockSuperAdminRole();
        var job = jobs.selectById(id);
        if (job == null || !"QUEUED".equals(job.getStatus())) return null;
        expire(job);
        if (!"QUEUED".equals(job.getStatus())) return null;
        Payload payload;
        try {
            payload = payload(job);
            actors.check(job.getActorUserId(), payload.sessionId());
            checkSource(payload.connection());
        } catch (CameraAdapterException ex) {
            finishFailed(job, ex.reasonCode());
            return null;
        }
        job.setStatus("RUNNING");
        advance(job);
        return new Work(
                id, job.getVersion(), job.getActorUserId(), job.getOriginTraceId(), payload);
    }

    /** The completed discovery may have been explicitly converted to a new queued import phase. */
    public boolean queued(long id) {
        var job = jobs.selectById(id);
        return job != null && "QUEUED".equals(job.getStatus());
    }

    /** Runs before each outbound protocol request; does not renew the actor's Redis session. */
    public void checkWork(Work work) {
        var current = jobs.selectById(work.id());
        if (current == null
                || current.getVersion() != work.version()
                || !"RUNNING".equals(current.getStatus())
                || !now().isBefore(current.getExpiresAt()))
            throw new CameraAdapterException("JOB_NO_LONGER_ACTIVE");
        actors.check(work.userId(), work.payload().sessionId());
        checkSource(work.payload().connection());
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void complete(Work work, CameraAccessCatalog catalog) {
        coordination.lockSuperAdminRole();
        checkWork(work);
        var job = jobs.selectById(work.id());
        encrypt(job, new Payload(work.payload().sessionId(), work.payload().connection(), catalog));
        job.setStatus(catalog.complete() ? "SUCCEEDED" : "PARTIAL");
        job.setMethod(catalog.adapterType());
        advance(job);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void completeScan(Work work, CameraNetworkScan.Result result) {
        coordination.lockSuperAdminRole();
        checkWork(work);
        var job = jobs.selectById(work.id());
        encrypt(
                job,
                new Payload(work.payload().sessionId(), null, null, work.payload().scan(), result));
        job.setStatus(result.complete() ? "SUCCEEDED" : "PARTIAL");
        advance(job);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void fail(long id, Long version, String reason) {
        coordination.lockSuperAdminRole();
        var job = jobs.selectById(id);
        if (job == null
                || !ACTIVE.contains(job.getStatus())
                || version != null && !version.equals(job.getVersion())) return;
        finishFailed(job, reason);
    }

    /** Queue rejection cannot terminate an already claimed worker. */
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void failQueued(long id, String reason) {
        coordination.lockSuperAdminRole();
        var job = jobs.selectById(id);
        if (job != null && "QUEUED".equals(job.getStatus())) finishFailed(job, reason);
    }

    private void finishFailed(CameraAccessJobEntity job, String reason) {
        job.setStatus("ACTOR_REVOKED".equals(reason) ? "CANCELLED" : "FAILED");
        job.setReasonCode(reason);
        advance(job);
        clearSecret(job);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void maintenance(boolean restarting) {
        coordination.lockSuperAdminRole();
        if (restarting)
            jobs.update(
                    null,
                    new LambdaUpdateWrapper<CameraAccessJobEntity>()
                            .in(CameraAccessJobEntity::getStatus, ACTIVE)
                            .set(CameraAccessJobEntity::getStatus, "FAILED")
                            .set(CameraAccessJobEntity::getReasonCode, "SERVER_RESTARTED")
                            .set(CameraAccessJobEntity::getUpdatedAt, now())
                            .setSql("version=version+1")
                            .set(CameraAccessJobEntity::getSecretCiphertext, null)
                            .set(CameraAccessJobEntity::getSecretNonce, null)
                            .set(CameraAccessJobEntity::getSecretTag, null)
                            .set(CameraAccessJobEntity::getEncryptionKeyId, null));
        // Purging expiring secrets and removing old receipts use separate batches to prevent
        // starvation.
        var rows =
                jobs.selectList(
                        new LambdaQueryWrapper<CameraAccessJobEntity>()
                                .ne(CameraAccessJobEntity::getStatus, "EXPIRED")
                                .and(
                                        q ->
                                                q.ne(
                                                                CameraAccessJobEntity::getJobKind,
                                                                "BULK_IMPORT")
                                                        .or()
                                                        .in(
                                                                CameraAccessJobEntity::getStatus,
                                                                ACTIVE))
                                .le(CameraAccessJobEntity::getExpiresAt, now())
                                .orderByAsc(CameraAccessJobEntity::getExpiresAt)
                                .last("LIMIT 100"));
        rows.forEach(this::expire);
        var old =
                jobs.selectList(
                        new LambdaQueryWrapper<CameraAccessJobEntity>()
                                .select(
                                        CameraAccessJobEntity::getId,
                                        CameraAccessJobEntity::getBulkProcessedCount)
                                .lt(CameraAccessJobEntity::getExpiresAt, now().minusDays(1))
                                .orderByAsc(CameraAccessJobEntity::getExpiresAt)
                                .last("LIMIT 100"));
        // A full bulk ticket owns up to 10k child receipts. Bound the cascading work as well
        // as the number of parent rows while retaining the existing coordination lock order.
        List<Long> purge = new ArrayList<>();
        int receiptBudget = 20_000;
        for (var job : old) {
            int receiptCount =
                    job.getBulkProcessedCount() == null ? 0 : job.getBulkProcessedCount();
            if (receiptCount > receiptBudget) break;
            purge.add(job.getId());
            receiptBudget -= receiptCount;
        }
        if (!purge.isEmpty()) jobs.deleteByIds(purge);
    }

    private CameraAccessJobEntity ready(String id, String version, String sessionId, long userId) {
        var job = owned(id, sessionId, userId);
        expire(job);
        requireReady(job, version);
        return job;
    }

    void requireReady(CameraAccessJobEntity job, String version) {
        if ("SCAN".equals(job.getMethod()) || "BULK_IMPORT".equals(job.getJobKind()))
            throw CameraSourceRules.invalid();
        if (!Set.of("SUCCEEDED", "PARTIAL").contains(job.getStatus())
                || job.getResultSummary() != null) throw error(ErrorCode.CONFLICT);
        if (job.getVersion() != DecimalInput.version(version, "version"))
            throw error(ErrorCode.VERSION_CONFLICT);
    }

    CameraAccessJobEntity owned(String id, String sessionId, long userId) {
        var job = jobs.selectById(DecimalInput.id(id, "jobId"));
        if (job == null || job.getActorUserId() != userId || !sameSession(job, sessionId))
            throw error(ErrorCode.NOT_FOUND);
        return job;
    }

    private boolean sameSession(CameraAccessJobEntity job, String sessionId) {
        return MessageDigest.isEqual(
                job.getActorSessionDigest(),
                crypto.digest(job.getDigestKeyId(), "session", sessionId));
    }

    private void checkSource(CameraConnection connection) {
        if (connection == null || connection.sourceId() == null) return;
        var source = sources.selectById(DecimalInput.id(connection.sourceId(), "sourceId"));
        if (source == null
                || !Boolean.TRUE.equals(source.getEnabled())
                || source.getVersion()
                        != DecimalInput.version(connection.sourceVersion(), "sourceVersion"))
            throw new CameraAdapterException("SOURCE_CHANGED");
    }

    void checkImportSource(CameraConnection connection) {
        try {
            checkSource(connection);
        } catch (CameraAdapterException ex) {
            throw error(ErrorCode.VERSION_CONFLICT);
        }
    }

    void expire(CameraAccessJobEntity job) {
        if ("BULK_IMPORT".equals(job.getJobKind()) && !ACTIVE.contains(job.getStatus())) return;
        if (!now().isBefore(job.getExpiresAt()) && !"EXPIRED".equals(job.getStatus())) {
            job.setStatus("EXPIRED");
            job.setReasonCode(
                    "BULK_IMPORT".equals(job.getJobKind())
                            ? "IMPORT_TIME_LIMIT_REACHED"
                            : "DISCOVERY_EXPIRED");
            advance(job);
            clearSecret(job);
        }
    }

    void advance(CameraAccessJobEntity job) {
        long previous = job.getVersion();
        job.setVersion(com.streamfusion.platform.common.validation.VersionCounter.next(previous));
        job.setUpdatedAt(now());
        if (jobs.update(
                        job,
                        new LambdaUpdateWrapper<CameraAccessJobEntity>()
                                .eq(CameraAccessJobEntity::getId, job.getId())
                                .eq(CameraAccessJobEntity::getVersion, previous))
                != 1) throw error(ErrorCode.VERSION_CONFLICT);
    }

    void clearSecret(CameraAccessJobEntity job) {
        jobs.update(
                null,
                new LambdaUpdateWrapper<CameraAccessJobEntity>()
                        .eq(CameraAccessJobEntity::getId, job.getId())
                        .set(CameraAccessJobEntity::getSecretCiphertext, null)
                        .set(CameraAccessJobEntity::getSecretNonce, null)
                        .set(CameraAccessJobEntity::getSecretTag, null)
                        .set(CameraAccessJobEntity::getEncryptionKeyId, null));
        job.setSecretCiphertext(null);
    }

    Payload payload(CameraAccessJobEntity job) {
        var node =
                crypto.decrypt(
                        aad(job),
                        new CameraCryptoService.Envelope(
                                job.getEncryptionKeyId(),
                                job.getSecretNonce(),
                                job.getSecretCiphertext(),
                                job.getSecretTag()));
        try {
            return json.treeToValue(node, Payload.class);
        } catch (Exception ex) {
            throw error(ErrorCode.DEPENDENCY_UNAVAILABLE);
        }
    }

    void encrypt(CameraAccessJobEntity job, Payload payload) {
        var envelope = crypto.encrypt(aad(job), payload, 1_048_576);
        job.setEncryptionKeyId(envelope.keyId());
        job.setSecretNonce(envelope.nonce());
        job.setSecretCiphertext(envelope.ciphertext());
        job.setSecretTag(envelope.tag());
    }

    private static String aad(CameraAccessJobEntity job) {
        return "camera_access_job/" + job.getId() + "/" + job.getActorUserId();
    }

    private Map<String, Object> result(CameraAccessJobEntity job) {
        try {
            return json.readValue(job.getResultSummary(), new TypeReference<>() {});
        } catch (Exception ex) {
            throw error(ErrorCode.INTERNAL_ERROR);
        }
    }

    Map<String, Object> view(CameraAccessJobEntity job) {
        Payload payload = job.getSecretCiphertext() == null ? null : payload(job);
        CameraAccessCatalog catalog = payload == null ? null : payload.catalog();
        CameraNetworkScan.Result scanResult = payload == null ? null : payload.scanResult();
        var value = new LinkedHashMap<String, Object>();
        value.put("jobId", job.getId().toString());
        value.put("version", job.getVersion().toString());
        value.put("status", job.getStatus());
        value.put("method", job.getMethod());
        value.put("kind", job.getJobKind());
        value.put(
                "sourceVersion",
                job.getSourceVersion() == null ? null : job.getSourceVersion().toString());
        if ("BULK_IMPORT".equals(job.getJobKind())) {
            var progress = new LinkedHashMap<String, Object>();
            progress.put(
                    "groupId",
                    job.getBulkGroupId() == null ? null : job.getBulkGroupId().toString());
            progress.put(
                    "pageNumber",
                    Set.of("SUCCEEDED", "PARTIAL").contains(job.getStatus())
                            ? Math.max(1, job.getBulkNextPage() - 1)
                            : job.getBulkNextPage());
            progress.put("total", job.getBulkTotal());
            progress.put("processedCount", job.getBulkProcessedCount());
            progress.put("createdCount", job.getBulkCreatedCount());
            progress.put("existingCount", job.getBulkExistingCount());
            progress.put("failedCount", job.getBulkFailedCount());
            progress.put("duplicateCount", job.getBulkDuplicateCount());
            progress.put("maxItems", CameraBulkImportService.MAX_ITEMS);
            value.put("bulk", progress);
        }
        value.put("hosts", scanResult == null ? List.of() : scanResult.hosts());
        value.put("scannedTargets", scanResult == null ? 0 : scanResult.scannedTargets());
        value.put(
                "totalTargets",
                payload == null || payload.scan() == null ? 0 : payload.scan().hosts().size());
        value.put("page", catalog == null ? null : catalog.page());
        value.put("sourceId", job.getSourceId() == null ? null : job.getSourceId().toString());
        value.put("expiresAt", job.getExpiresAt().atZone(ZONE).toInstant());
        value.put(
                "complete",
                "BULK_IMPORT".equals(job.getJobKind())
                        ? "SUCCEEDED".equals(job.getStatus())
                        : scanResult != null
                                ? scanResult.complete()
                                : catalog != null && catalog.complete());
        value.put(
                "device",
                catalog == null || catalog.device() == null
                        ? null
                        : Map.of(
                                "name",
                                safe(catalog.device().name()),
                                "manufacturer",
                                safe(catalog.device().manufacturer()),
                                "model",
                                safe(catalog.device().model()),
                                "firmware",
                                safe(catalog.device().firmware()),
                                "serialNumber",
                                safe(catalog.device().serialNumber())));
        var candidates = new ArrayList<Map<String, Object>>();
        if (catalog != null)
            for (int i = 0; i < catalog.channels().size(); i++) {
                var channel = catalog.channels().get(i);
                var profiles = new ArrayList<Map<String, Object>>();
                for (int p = 0; p < channel.profiles().size(); p++) {
                    var profile = channel.profiles().get(p);
                    var v = new LinkedHashMap<String, Object>();
                    v.put("profileId", "p" + p);
                    v.put("name", safe(profile.name()));
                    v.put("usageHint", profile.usageHint());
                    v.put("videoCodec", profile.codec());
                    v.put("width", profile.width());
                    v.put("height", profile.height());
                    v.put("frameRate", profile.frameRate());
                    v.put("bitrateKbps", profile.bitrateKbps());
                    profiles.add(v);
                }
                candidates.add(
                        Map.of(
                                "candidateId",
                                "c" + i,
                                "name",
                                safe(channel.name()),
                                "mappingRequired",
                                channel.mappingRequired(),
                                "profiles",
                                profiles));
            }
        value.put("candidates", candidates);
        value.put(
                "warnings",
                scanResult != null && !scanResult.complete()
                        ? List.of("SCAN_TIME_LIMIT_REACHED")
                        : catalog == null ? List.of() : catalog.warnings());
        value.put(
                "diagnostic",
                job.getReasonCode() == null
                        ? null
                        : Map.of(
                                "reasonCode",
                                job.getReasonCode(),
                                "actionHint",
                                hint(job.getReasonCode()),
                                "originTraceId",
                                safe(job.getOriginTraceId()),
                                "occurredAt",
                                job.getUpdatedAt().atZone(ZONE).toInstant()));
        value.put("result", job.getResultSummary() == null ? null : result(job));
        return value;
    }

    private static String hint(String code) {
        return switch (code) {
            case "AUTHENTICATION_FAILED" -> "请核对设备账户、密码以及ONVIF独立账户配置后重试";
            case "ACTOR_REVOKED" -> "当前登录或权限已失效，请重新登录并确认相机管理权限";
            case "SOURCE_CHANGED" -> "接入来源已变更，请重新读取设备目录";
            case "IMPORT_SCOPE_CHANGED" -> "视频组或账户授权已变化，已导入结果保留；请核对范围后重新确认导入";
            case "IMPORT_TIME_LIMIT_REACHED" -> "本次后台导入已到30分钟期限，已导入结果保留；可重新读取同一来源后重试";
            case "IMPORT_LIMIT_REACHED" -> "已到10000条或100页上限，已导入结果保留；同一全目录重试不会继续超过本次上限";
            case "IMPORT_ITEM_FAILED" -> "部分条目未能导入，请查看失败明细；其他已导入相机保留";
            case "DIRECTORY_CHANGED", "DIRECTORY_INCOMPLETE" ->
                    "平台目录发生变化或返回不完整，已导入结果保留；核对平台目录后可重试同一来源";
            case "IMPORT_UNAVAILABLE" -> "后台导入中断，已提交结果保留；请凭请求标识检查日志后重试";
            case "SERVER_RESTARTED", "DISCOVERY_EXPIRED" -> "请重新连接设备读取目录";
            default -> "请核对接入方式、设备地址和网络配置后重试；详情可凭请求标识定位";
        };
    }

    private static String safe(String value) {
        return value == null ? "" : value;
    }

    private void requestTime(String id, boolean fresh) {
        Instant requested = CameraRequestKey.timestamp(id);
        if (fresh) CameraRequestKey.requireFresh(requested, clock.instant());
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZONE);
    }

    private static BusinessException error(ErrorCode code) {
        return BusinessException.error(code);
    }
}
