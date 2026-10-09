package com.streamfusion.platform.camera.access.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.mapper.CameraAccessImportItemMapper;
import com.streamfusion.platform.camera.access.pojo.*;
import com.streamfusion.platform.camera.mapper.CameraAccessJobMapper;
import com.streamfusion.platform.camera.service.*;
import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.security.MessageDigest;
import java.time.*;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.*;

/** Short transactions persist one platform resource and its receipt; no network IO here. */
@Service
@RequiredArgsConstructor
public class CameraBulkImportService {
    public static final int MAX_ITEMS = 10000;
    public static final int PAGE_SIZE = 100;
    public static final int MAX_PAGES = 100;
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraAccessJobsService jobs;
    private final CameraAccessJobMapper rows;
    private final CameraAccessImportItemMapper items;
    private final CameraAccessImportService imports;
    private final CameraSourceService sources;
    private final CameraGroupService groups;
    private final CameraAccessService access;
    private final AccessMapper coordination;
    private final CameraAccessAdapterRegistry adapters;
    private final CameraCryptoService crypto;
    private final Clock clock;

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> preview(
            String id, String version, String groupId, String sessionId) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var job = jobs.owned(id, sessionId, actor.userId());
        jobs.expire(job);
        jobs.requireReady(job, version);
        var payload = requirePlatform(job);
        Long group = group(groupId);
        // The existing placement calculation needs only whether future new assets are possible;
        // it does not assert that the platform's declared total is the number of new local assets.
        var result =
                new LinkedHashMap<>(
                        groups.previewImport(
                                actor,
                                job.getId(),
                                job.getVersion(),
                                group,
                                1,
                                proof(payload, group)));
        result.put("declaredTotal", payload.catalog().page().total());
        result.put("cameraCount", payload.catalog().page().total());
        result.put("maxItems", MAX_ITEMS);
        result.put("pageSize", PAGE_SIZE);
        return result;
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public Map<String, Object> start(
            String id,
            String version,
            String groupId,
            String confirmation,
            String requestId,
            String sessionId,
            AuditContextDto audit) {
        var actor = access.lockActor();
        access.requireSuper(actor);
        var job = jobs.owned(id, sessionId, actor.userId());
        jobs.expire(job);
        Instant requested = CameraRequestKey.timestamp(requestId);
        Long group = group(groupId);
        byte[] fingerprint =
                crypto.digest(
                        job.getDigestKeyId(), "access-bulk-import", Arrays.asList(version, group));
        if ("BULK_IMPORT".equals(job.getJobKind())) {
            if (!requestId.equals(job.getImportRequestId())
                    || !MessageDigest.isEqual(fingerprint, job.getImportFingerprint()))
                throw BusinessException.error(ErrorCode.CONFLICT);
            return jobs.view(job);
        }
        CameraRequestKey.requireFresh(requested, clock.instant());
        jobs.requireReady(job, version);
        var payload = requirePlatform(job);
        if (rows.selectCount(
                                new LambdaQueryWrapper<CameraAccessJobEntity>()
                                        .eq(CameraAccessJobEntity::getActorUserId, actor.userId())
                                        .in(CameraAccessJobEntity::getStatus, "QUEUED", "RUNNING")
                                        .gt(CameraAccessJobEntity::getExpiresAt, now()))
                        >= 4
                || rows.selectCount(
                                new LambdaQueryWrapper<CameraAccessJobEntity>()
                                        .in(CameraAccessJobEntity::getStatus, "QUEUED", "RUNNING")
                                        .gt(CameraAccessJobEntity::getExpiresAt, now()))
                        >= 20) throw BusinessException.error(ErrorCode.RATE_LIMITED);
        groups.verifyImport(
                actor, job.getId(), job.getVersion(), group, proof(payload, group), confirmation);
        var source =
                sources.createImported(
                        payload.connection(), payload.catalog().adapterType(), actor, audit);
        var c = payload.connection();
        var fixed =
                new CameraConnection(
                        payload.catalog().adapterType(),
                        c.name(),
                        c.host(),
                        c.port(),
                        c.scheme(),
                        c.username(),
                        c.password(),
                        c.rtspPort(),
                        c.networkPolicyKey(),
                        source.getId().toString(),
                        source.getVersion().toString(),
                        c.rtspUrls(),
                        1,
                        PAGE_SIZE);
        var control =
                new CameraAccessJobsService.BulkControl(
                        group,
                        groups.importScopeFingerprint(actor, group),
                        audit.getSourceIp(),
                        audit.getClientSummary());
        jobs.encrypt(
                job,
                new CameraAccessJobsService.Payload(
                        payload.sessionId(), fixed, null, null, null, control));
        job.setJobKind("BULK_IMPORT");
        job.setStatus("QUEUED");
        job.setMethod(payload.catalog().adapterType());
        job.setSourceId(source.getId());
        job.setSourceVersion(source.getVersion());
        job.setBulkGroupId(group);
        job.setBulkNextPage(1);
        job.setBulkTotal(payload.catalog().page().total());
        job.setBulkProcessedCount(0);
        job.setBulkCreatedCount(0);
        job.setBulkExistingCount(0);
        job.setBulkFailedCount(0);
        job.setBulkDuplicateCount(0);
        job.setBulkStartedAt(now());
        job.setExpiresAt(now().plusMinutes(30));
        job.setImportRequestId(requestId);
        job.setImportFingerprint(fingerprint);
        jobs.advance(job);
        return jobs.view(job);
    }

    private CameraAccessJobsService.Payload requirePlatform(CameraAccessJobEntity job) {
        var payload = jobs.payload(job);
        var catalog = payload.catalog();
        if (catalog == null
                || catalog.page() == null
                || catalog.page().total() == null
                || catalog.page().pageNumber() != 1
                || !adapters.supportsCategory(catalog.adapterType(), "PLATFORM")
                || !adapters.descriptor(catalog.adapterType()).paged())
            throw CameraSourceRules.invalid();
        jobs.checkImportSource(payload.connection());
        return payload;
    }

    private String proof(CameraAccessJobsService.Payload payload, Long groupId) {
        return crypto.sign(
                "camera-bulk-import-choice",
                Arrays.asList(
                        payload.connection(), payload.catalog(), groupId, MAX_ITEMS, PAGE_SIZE));
    }

    /**
     * Checks persisted actor, source version and approved tree/scope before each request or write.
     */
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void checkWork(CameraAccessJobsService.Work work) {
        coordination.lockSuperAdminRole();
        guarded(work);
    }

    private CameraAccessJobEntity guarded(CameraAccessJobsService.Work work) {
        jobs.checkWork(work);
        var control = work.payload().bulk();
        if (control == null) throw new CameraAdapterException("JOB_NO_LONGER_ACTIVE");
        try {
            if (!groups.matchesImportScope(
                    new CameraAccessService.Actor(work.userId(), true),
                    control.groupId(),
                    control.scopeFingerprint()))
                throw new CameraAdapterException("IMPORT_SCOPE_CHANGED");
        } catch (BusinessException ex) {
            throw new CameraAdapterException("IMPORT_SCOPE_CHANGED");
        }
        return rows.selectById(work.id());
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraAccessJobsService.Work beginPage(
            CameraAccessJobsService.Work work, CameraAccessCatalog page) {
        coordination.lockSuperAdminRole();
        var job = guarded(work);
        var meta = page.page();
        if (meta == null
                || meta.pageNumber() != job.getBulkNextPage()
                || meta.pageSize() != PAGE_SIZE
                || !page.adapterType().equals(job.getMethod())
                || page.channels().size() > PAGE_SIZE
                || meta.total() == null
                || meta.total() < 0
                || page.channels().stream()
                        .anyMatch(
                                c ->
                                        c.externalKey() == null
                                                || c.externalKey().isBlank()
                                                || c.externalKey().length() > 512)
                || meta.hasMore() && page.channels().isEmpty())
            throw new CameraAdapterException("DIRECTORY_INCOMPLETE");
        if (!Objects.equals(meta.total(), job.getBulkTotal()) || !page.complete())
            job.setReasonCode("DIRECTORY_CHANGED");
        job.setBulkTotal(meta.total());
        jobs.advance(job);
        return work(job, work);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraAccessJobsService.Work importItem(
            CameraAccessJobsService.Work work, CameraAccessCatalog page, int index) {
        coordination.lockSuperAdminRole();
        var job = guarded(work);
        if (job.getBulkProcessedCount() >= MAX_ITEMS)
            throw new CameraAdapterException("IMPORT_LIMIT_REACHED");
        var channel = page.channels().get(index);
        if (known(job.getId(), channel.externalKey())) {
            job.setBulkDuplicateCount(job.getBulkDuplicateCount() + 1);
        } else {
            var control = work.payload().bulk();
            var single =
                    new CameraAccessCatalog(
                                    page.adapterType(),
                                    page.device(),
                                    List.of(channel),
                                    page.complete(),
                                    page.warnings())
                            .observedAt(clock.instant());
            var result =
                    imports.executeBackground(
                            work.payload().connection(),
                            single,
                            control.groupId(),
                            new CameraAccessService.Actor(work.userId(), true),
                            new AuditContextDto(control.sourceIp(), control.clientSummary()));
            var camera = result.cameras().getFirst();
            String status = camera.status().name();
            receipt(job, page, index, status, camera.cameraId(), null);
            job.setBulkProcessedCount(job.getBulkProcessedCount() + 1);
            if ("CREATED".equals(status)) job.setBulkCreatedCount(job.getBulkCreatedCount() + 1);
            else job.setBulkExistingCount(job.getBulkExistingCount() + 1);
        }
        jobs.advance(job);
        return work(job, work);
    }

    /**
     * The failed asset transaction has already rolled back before this separate receipt commits.
     */
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraAccessJobsService.Work failedItem(
            CameraAccessJobsService.Work work, CameraAccessCatalog page, int index, String reason) {
        coordination.lockSuperAdminRole();
        var job = guarded(work);
        if (known(job.getId(), page.channels().get(index).externalKey())) {
            job.setBulkDuplicateCount(job.getBulkDuplicateCount() + 1);
        } else {
            receipt(job, page, index, "FAILED", null, reason);
            job.setBulkProcessedCount(job.getBulkProcessedCount() + 1);
            job.setBulkFailedCount(job.getBulkFailedCount() + 1);
        }
        jobs.advance(job);
        return work(job, work);
    }

    private boolean known(long jobId, String key) {
        return items.selectCount(
                        new LambdaQueryWrapper<CameraAccessImportItemEntity>()
                                .eq(CameraAccessImportItemEntity::getJobId, jobId)
                                .eq(CameraAccessImportItemEntity::getExternalKey, key))
                > 0;
    }

    private void receipt(
            CameraAccessJobEntity job,
            CameraAccessCatalog page,
            int index,
            String status,
            Long cameraId,
            String reason) {
        var channel = page.channels().get(index);
        var item = new CameraAccessImportItemEntity();
        item.setJobId(job.getId());
        item.setExternalKey(channel.externalKey());
        item.setPageNumber(page.page().pageNumber());
        item.setItemIndex(index);
        item.setStatus(status);
        item.setCameraId(cameraId);
        item.setName(
                channel.name() == null
                        ? null
                        : channel.name()
                                .codePoints()
                                .limit(128)
                                .collect(
                                        StringBuilder::new,
                                        StringBuilder::appendCodePoint,
                                        StringBuilder::append)
                                .toString());
        item.setReasonCode(reason);
        item.setCreatedAt(now());
        items.insert(item);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public CameraAccessJobsService.Work finishPage(
            CameraAccessJobsService.Work work, CameraAccessCatalog page) {
        coordination.lockSuperAdminRole();
        var job = guarded(work);
        job.setBulkNextPage(page.page().pageNumber() + 1);
        if (!page.page().hasMore()
                || job.getBulkNextPage() > MAX_PAGES
                || job.getBulkProcessedCount() >= MAX_ITEMS
                || !page.complete()) {
            if (!page.complete()) job.setReasonCode("DIRECTORY_INCOMPLETE");
            else if (page.page().hasMore()) job.setReasonCode("IMPORT_LIMIT_REACHED");
            else if (job.getBulkProcessedCount().longValue() != job.getBulkTotal().longValue()
                    || job.getBulkDuplicateCount() > 0) job.setReasonCode("DIRECTORY_CHANGED");
            if (job.getBulkFailedCount() > 0 && job.getReasonCode() == null)
                job.setReasonCode("IMPORT_ITEM_FAILED");
            job.setStatus(job.getReasonCode() == null ? "SUCCEEDED" : "PARTIAL");
            jobs.advance(job);
            jobs.clearSecret(job);
            return null;
        }
        jobs.advance(job);
        return work(job, work);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public PageResultVo<Map<String, Object>> list(
            String kind, int page, int size, String sessionId) {
        bounds(page, size);
        var actor = access.lockActor();
        access.requireSuper(actor);
        if (!"BULK_IMPORT".equals(kind)) throw CameraSourceRules.invalid();
        var query =
                new LambdaQueryWrapper<CameraAccessJobEntity>()
                        .eq(CameraAccessJobEntity::getActorUserId, actor.userId())
                        .eq(CameraAccessJobEntity::getJobKind, kind);
        var keys = crypto.keyIds();
        if (keys.isEmpty()) return new PageResultVo<>(List.of(), page, size, 0);
        query.and(
                q -> {
                    for (String key : keys)
                        q.or(
                                c ->
                                        c.eq(CameraAccessJobEntity::getDigestKeyId, key)
                                                .eq(
                                                        CameraAccessJobEntity
                                                                ::getActorSessionDigest,
                                                        crypto.digest(key, "session", sessionId)));
                });
        long total = rows.selectCount(query);
        var found =
                rows.selectList(
                        query.orderByDesc(CameraAccessJobEntity::getUpdatedAt)
                                .orderByDesc(CameraAccessJobEntity::getId)
                                .last("LIMIT " + size + " OFFSET " + ((long) (page - 1) * size)));
        found.forEach(jobs::expire);
        return new PageResultVo<>(found.stream().map(jobs::view).toList(), page, size, total);
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public PageResultVo<Map<String, Object>> items(
            String id, String status, int page, int size, String sessionId) {
        bounds(page, size);
        var actor = access.lockActor();
        access.requireSuper(actor);
        var job = jobs.owned(id, sessionId, actor.userId());
        if (!"BULK_IMPORT".equals(job.getJobKind())) throw CameraSourceRules.invalid();
        if (status != null && !Set.of("CREATED", "EXISTING", "FAILED").contains(status))
            throw CameraSourceRules.invalid();
        var query =
                new LambdaQueryWrapper<CameraAccessImportItemEntity>()
                        .eq(CameraAccessImportItemEntity::getJobId, job.getId())
                        .eq(status != null, CameraAccessImportItemEntity::getStatus, status);
        long total = items.selectCount(query);
        var found =
                items.selectList(
                        query.orderByAsc(CameraAccessImportItemEntity::getId)
                                .last("LIMIT " + size + " OFFSET " + ((long) (page - 1) * size)));
        return new PageResultVo<>(found.stream().map(this::itemView).toList(), page, size, total);
    }

    private Map<String, Object> itemView(CameraAccessImportItemEntity item) {
        var map = new LinkedHashMap<String, Object>();
        map.put("id", item.getId().toString());
        map.put("externalKey", item.getExternalKey());
        map.put("pageNumber", item.getPageNumber());
        map.put("itemIndex", item.getItemIndex());
        map.put("status", item.getStatus());
        map.put("cameraId", item.getCameraId() == null ? null : item.getCameraId().toString());
        map.put("name", item.getName());
        map.put("reasonCode", item.getReasonCode());
        map.put("createdAt", item.getCreatedAt().atZone(ZONE).toInstant());
        return map;
    }

    private static void bounds(int page, int size) {
        if (page < 1 || page > 100000 || size < 1 || size > 100) throw CameraSourceRules.invalid();
    }

    private static Long group(String value) {
        return value == null ? null : DecimalInput.id(value, "groupId");
    }

    private static CameraAccessJobsService.Work work(
            CameraAccessJobEntity job, CameraAccessJobsService.Work old) {
        return new CameraAccessJobsService.Work(
                old.id(), job.getVersion(), old.userId(), old.traceId(), old.payload());
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZONE);
    }
}
