package com.streamfusion.platform.inspection.evidence;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.query.QueryWrapper;
import com.baomidou.mybatisplus.core.toolkit.IdWorker;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.mapper.CameraScopeMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.status.CameraStatusPlanEntity;
import com.streamfusion.platform.camera.status.CameraStatusPlanMapper;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.storage.StorageConfigurationService;
import com.streamfusion.platform.storage.entity.StorageDefault;
import com.streamfusion.platform.storage.entity.StorageInstance;
import com.streamfusion.platform.storage.entity.StorageScope;
import com.streamfusion.platform.storage.mapper.StorageDefaultMapper;
import com.streamfusion.platform.storage.mapper.StorageInstanceMapper;
import com.streamfusion.platform.storage.mapper.StorageScopeMapper;
import com.streamfusion.platform.storage.s3.StorageNetwork;
import com.streamfusion.platform.storage.s3.StorageS3Client;
import com.streamfusion.platform.system.access.mapper.AccessMapper;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.pojo.enums.UserStatus;
import com.streamfusion.platform.system.user.service.UserService;
import java.io.InputStream;
import java.security.MessageDigest;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.time.temporal.ChronoUnit;
import java.util.Base64;
import java.util.HexFormat;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.support.TransactionTemplate;

/** Short DB reservations and separately bounded remote verification; no video bytes enter JSON. */
@Service
@RequiredArgsConstructor
public class InspectionEvidenceService {
    private static final long MAX_OBJECT_BYTES = 64L * 1024 * 1024;
    private static final Set<String> IMAGES = Set.of("image/jpeg", "image/png");
    private static final Set<String> VIDEOS = Set.of("video/x-matroska", "video/mp4");

    private final InspectionEvidenceProperties properties;
    private final InspectionEvidenceMapper evidence;
    private final InspectionRunMapper runs;
    private final InspectionControlMapper controls;
    private final CameraStatusPlanMapper plans;
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraScopeMapper cameraScopes;
    private final CameraAccessService cameraAccess;
    private final AccessMapper coordination;
    private final AccessService access;
    private final CurrentUserService current;
    private final UserService users;
    private final StorageDefaultMapper defaults;
    private final StorageInstanceMapper instances;
    private final StorageScopeMapper scopes;
    private final StorageConfigurationService storage;
    private final StorageS3Client s3;
    private final StorageNetwork network;
    private final ObjectMapper json;
    private final TransactionTemplate transactions;

    public InspectionEvidenceCommands.Grant authorize(
            String nodeId, InspectionEvidenceCommands.Authorize input) {
        validate(input);
        return transactions.execute(
                ignored -> {
                    var bound = boundRun(nodeId, input.runId(), input.assignmentEpoch(), true);
                    var existing = latest(input.evidenceId(), true);
                    if (existing != null) {
                        validateIdempotent(existing, input);
                        return grant(existing, input.traceId());
                    }
                    if (properties.getMaxAccountedBytesPerScope() == 0)
                        throw unavailable("EVIDENCE_BUDGET_UNCONFIGURED");
                    StorageDefault selected = storage.defaultRow();
                    if (selected.getScopeId() == null)
                        throw unavailable("EVIDENCE_STORE_NOT_SELECTED");
                    StorageScope candidate = storage.scope(selected.getScopeId());
                    StorageInstance instance = storage.lock(candidate.getInstanceId());
                    StorageDefault currentDefault =
                            defaults.selectOne(
                                    new LambdaQueryWrapper<StorageDefault>()
                                            .eq(StorageDefault::getId, 1L)
                                            .last("FOR UPDATE"));
                    if (currentDefault == null
                            || !candidate.getId().equals(currentDefault.getScopeId()))
                        throw conflict("EVIDENCE_STORE_CHANGED");
                    StorageScope scope =
                            scopes.selectOne(
                                    new LambdaQueryWrapper<StorageScope>()
                                            .eq(StorageScope::getId, candidate.getId())
                                            .last("FOR UPDATE"));
                    if (scope == null || !scope.getInstanceId().equals(instance.getId()))
                        throw conflict("EVIDENCE_SCOPE_CHANGED");
                    var concurrent = latest(input.evidenceId(), true);
                    if (concurrent != null) {
                        validateIdempotent(concurrent, input);
                        return grant(concurrent, input.traceId());
                    }
                    if (!Boolean.TRUE.equals(instance.getWriteEnabled())
                            || !"https"
                                    .equalsIgnoreCase(
                                            java.net
                                                    .URI
                                                    .create(instance.getEndpoint())
                                                    .getScheme()))
                        throw unavailable("EVIDENCE_STORE_NOT_WRITABLE");
                    long used = accounted(scope.getId());
                    if (used > properties.getMaxAccountedBytesPerScope() - input.sizeBytes())
                        throw unavailable("EVIDENCE_APPLICATION_BUDGET_EXCEEDED");
                    Instant now = Instant.now().truncatedTo(ChronoUnit.SECONDS);
                    var row = new InspectionEvidence();
                    row.setId(IdWorker.getId());
                    row.setEvidenceId(input.evidenceId());
                    row.setRunId(input.runId());
                    row.setAssignmentEpoch(input.assignmentEpoch());
                    row.setCameraId(bound.cameraId());
                    row.setProfileId(bound.profileId());
                    row.setKind(input.kind());
                    row.setPlacementRevision(1);
                    row.setInstanceId(instance.getId());
                    row.setScopeId(scope.getId());
                    row.setBucket(scope.getBucket());
                    String suffix =
                            input.kind().equals("IMAGE")
                                    ? input.contentType().equals("image/png") ? ".png" : ".jpg"
                                    : input.contentType().equals("video/x-matroska")
                                            ? ".mkv"
                                            : ".mp4";
                    row.setObjectKey(
                            scope.getPrefix()
                                    + "inspections/"
                                    + input.runId()
                                    + "/"
                                    + input.evidenceId()
                                    + "/1"
                                    + suffix);
                    if (row.getObjectKey().getBytes(java.nio.charset.StandardCharsets.UTF_8).length
                            > 1024) throw invalid();
                    if (row.getObjectKey().contains("%")
                            || row.getObjectKey().contains("\\")
                            || java.util.Arrays.stream(row.getObjectKey().split("/", -1))
                                    .anyMatch(
                                            part ->
                                                    part.isEmpty()
                                                            || part.equals(".")
                                                            || part.equals(".."))) throw invalid();
                    row.setEndpointRevision(instance.getVersion() + 1);
                    row.setSizeBytes(input.sizeBytes());
                    row.setContentType(input.contentType());
                    row.setSha256(hex(input.checksumBase64()));
                    row.setState("RESERVED");
                    row.setReasonCode(null);
                    row.setRequestId(input.requestId());
                    row.setCreatedAt(utc(now));
                    row.setUpdatedAt(utc(now));
                    row.setExpiresAt(utc(now.plusSeconds(600)));
                    evidence.insert(row);
                    return grant(row, input.traceId());
                });
    }

    public InspectionEvidenceCommands.Completion complete(
            String nodeId, InspectionEvidenceCommands.Complete input) {
        if (input == null
                || !identifier(input.requestId())
                || !identifier(input.evidenceId())
                || !identifier(input.runId())
                || !identifier(input.traceId())
                || input.assignmentEpoch() == null
                || input.assignmentEpoch() < 1
                || input.placementRevision() == null
                || input.placementRevision() < 1
                || input.status() == null
                || !Set.of("NOT_SENT", "PUT_ACCEPTED", "REJECTED", "UNKNOWN")
                        .contains(input.status())) throw invalid();
        InspectionEvidence row =
                transactions.execute(
                        ignored -> {
                            boundRun(nodeId, input.runId(), input.assignmentEpoch(), false);
                            var found =
                                    placement(input.evidenceId(), input.placementRevision(), false);
                            if (found == null
                                    || !found.getRunId().equals(input.runId())
                                    || !found.getAssignmentEpoch().equals(input.assignmentEpoch()))
                                throw BusinessException.error(ErrorCode.NOT_FOUND);
                            return found;
                        });
        if ("COMMITTED".equals(row.getState())) return completion(row, "CONFIRMED");
        String reason = "REMOTE_UNVERIFIED";
        StorageS3Client.ObjectInfo object = null;
        try {
            var instance = storage.require(row.getInstanceId());
            object = s3.head(instance, row.getBucket(), row.getObjectKey());
            if (object.size() != row.getSizeBytes()
                    || !row.getContentType().equals(object.contentType())
                    || input.etag() != null && !input.etag().equals(object.etag())
                    || input.objectVersion() != null
                            && !input.objectVersion().equals(object.objectVersionId())
                    || !sameBytes(instance, row, object)) {
                object = null;
                reason = "EVIDENCE_REMOTE_MISMATCH";
            }
        } catch (RuntimeException ex) {
            object = null;
            reason =
                    "NOT_SENT".equals(input.status())
                            ? "EVIDENCE_NOT_SENT"
                            : "EVIDENCE_REMOTE_UNAVAILABLE";
        }
        final StorageS3Client.ObjectInfo confirmed = object;
        final String unresolved = reason;
        return transactions.execute(
                ignored -> {
                    boundRun(nodeId, input.runId(), input.assignmentEpoch(), false);
                    var locked = placement(input.evidenceId(), input.placementRevision(), true);
                    if (locked == null || !locked.getRunId().equals(input.runId()))
                        throw BusinessException.error(ErrorCode.NOT_FOUND);
                    if ("COMMITTED".equals(locked.getState()))
                        return completion(locked, "CONFIRMED");
                    locked.setState(confirmed == null ? "UNKNOWN" : "COMMITTED");
                    locked.setReasonCode(confirmed == null ? unresolved : "CONFIRMED");
                    locked.setUpdatedAt(utc(Instant.now()));
                    if (confirmed != null) {
                        locked.setCommittedAt(locked.getUpdatedAt());
                        locked.setEtag(confirmed.etag());
                        locked.setObjectVersionId(confirmed.objectVersionId());
                    }
                    evidence.updateById(locked);
                    return completion(locked, confirmed == null ? unresolved : "CONFIRMED");
                });
    }

    private InspectionEvidenceCommands.Grant grant(InspectionEvidence row, String traceId) {
        if ("COMMITTED".equals(row.getState())) throw conflict("EVIDENCE_ALREADY_COMMITTED");
        Instant now = Instant.now().truncatedTo(ChronoUnit.SECONDS);
        long seconds =
                java.time.Duration.between(now, row.getExpiresAt().toInstant(ZoneOffset.UTC))
                        .getSeconds();
        if (seconds < 1) throw conflict("EVIDENCE_GRANT_EXPIRED");
        var instance = storage.require(row.getInstanceId());
        if (!Boolean.TRUE.equals(instance.getWriteEnabled())
                || instance.getVersion() + 1 != row.getEndpointRevision())
            throw unavailable("EVIDENCE_STORE_NOT_WRITABLE");
        network.validateEndpoint(java.net.URI.create(instance.getEndpoint()));
        String checksum =
                Base64.getEncoder().encodeToString(HexFormat.of().parseHex(row.getSha256()));
        var signed =
                s3.presignedPut(
                        instance,
                        row.getBucket(),
                        row.getObjectKey(),
                        row.getSizeBytes(),
                        row.getContentType(),
                        checksum,
                        (int) Math.min(900, seconds),
                        now);
        return new InspectionEvidenceCommands.Grant(
                row.getEvidenceId(),
                row.getRunId(),
                row.getAssignmentEpoch(),
                row.getPlacementRevision(),
                traceId,
                row.getInstanceId().toString(),
                row.getScopeId().toString(),
                row.getBucket(),
                row.getObjectKey(),
                row.getEndpointRevision(),
                now.plusSeconds(Math.min(900, seconds)),
                row.getSizeBytes(),
                row.getContentType(),
                "SHA256",
                checksum,
                signed.url(),
                signed.headers());
    }

    private InspectionEvidenceCommands.Completion completion(
            InspectionEvidence row, String reason) {
        boolean committed = "COMMITTED".equals(row.getState());
        return new InspectionEvidenceCommands.Completion(
                row.getEvidenceId(),
                row.getPlacementRevision(),
                row.getState(),
                reason,
                committed ? row.getId().toString() : null,
                committed ? row.getSizeBytes() : null,
                committed ? row.getContentType() : null,
                committed
                        ? Base64.getEncoder()
                                .encodeToString(HexFormat.of().parseHex(row.getSha256()))
                        : null,
                row.getObjectVersionId());
    }

    private record Bound(long cameraId, long profileId) {}

    private Bound boundRun(String nodeId, String runId, Integer epoch, boolean requireActive) {
        // Camera grants and plan STOP use this same coordination lock. Take it before any
        // non-locking read so authorization observes their committed state.
        coordination.lockSuperAdminRole();
        var run = runs.selectById(runId);
        if (nodeId == null
                || run == null
                || !nodeId.equals(run.getNodeId())
                || !epoch.equals(run.getAssignmentEpoch()))
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        var control =
                controls.selectOne(
                        new LambdaQueryWrapper<InspectionControl>()
                                .eq(InspectionControl::getControlId, run.getControlId())
                                .last("FOR UPDATE"));
        run =
                runs.selectOne(
                        new LambdaQueryWrapper<InspectionRun>()
                                .eq(InspectionRun::getRunId, runId)
                                .last("FOR UPDATE"));
        if (run == null
                || !nodeId.equals(run.getNodeId())
                || !epoch.equals(run.getAssignmentEpoch()))
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (control == null || !nodeId.equals(control.getNodeId()))
            throw conflict("EVIDENCE_CONTROL_STALE");
        if (requireActive
                && (!run.getBootId().equals(control.getTargetBootId())
                        || !run.getControlSeq().equals(control.getControlSeq())
                        || !"RUNNING".equals(control.getDesiredState())
                        || !"RUNNING".equals(run.getState())
                        || Boolean.TRUE.equals(run.getResourcesReleased())
                        || !utc(Instant.now()).isBefore(control.getExpiresAt())
                        || !utc(Instant.now()).isBefore(run.getLeaseExpiresAt())))
            throw conflict("EVIDENCE_RUN_INACTIVE");
        JsonNode spec;
        try {
            spec = json.readTree(run.getSpecJson());
        } catch (Exception ex) {
            throw conflict("EVIDENCE_SPEC_INVALID");
        }
        long planId = numeric(spec.path("planId").asText(null));
        long cameraId = numeric(spec.path("targetId").asText(null));
        long profileId = numeric(spec.path("profileId").asText(null));
        if (!Long.toString(planId).equals(control.getPlanId()))
            throw conflict("EVIDENCE_PLAN_MISMATCH");
        CameraStatusPlanEntity plan = plans.lock(planId);
        if (plan == null || plan.getActorUserId() == null)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (requireActive && !"ACTIVE".equals(plan.getState()))
            throw conflict("EVIDENCE_PLAN_INACTIVE");
        var owner = users.lockById(plan.getActorUserId());
        if (owner == null
                || owner.getStatus() != UserStatus.NORMAL.getCode()
                || CurrentUserService.requiresPasswordChange(owner)
                || !current.hasModuleAccess(owner.getId(), "camera"))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        CameraChannelEntity camera =
                channels.selectOne(
                        new LambdaQueryWrapper<CameraChannelEntity>()
                                .eq(CameraChannelEntity::getId, cameraId)
                                .last("FOR UPDATE"));
        CameraProfileEntity profile =
                profiles.selectOne(
                        new LambdaQueryWrapper<CameraProfileEntity>()
                                .eq(CameraProfileEntity::getId, profileId)
                                .last("FOR UPDATE"));
        if (camera == null
                || !"ENABLED".equals(camera.getLifecycle())
                || profile == null
                || !Boolean.TRUE.equals(profile.getEnabled())
                || !cameraIdEquals(profile.getChannelId(), cameraId)
                || cameraScopes.visibleCamera(
                                cameraId,
                                cameraAccess.visibility(
                                        new CameraAccessService.Actor(
                                                owner.getId(), access.isSuperAdmin(owner.getId()))))
                        != 1) throw BusinessException.error(ErrorCode.FORBIDDEN);
        return new Bound(cameraId, profileId);
    }

    private static boolean cameraIdEquals(Long id, long expected) {
        return id != null && id == expected;
    }

    private InspectionEvidence latest(String id, boolean lock) {
        return evidence.selectOne(
                new LambdaQueryWrapper<InspectionEvidence>()
                        .eq(InspectionEvidence::getEvidenceId, id)
                        .orderByDesc(InspectionEvidence::getPlacementRevision)
                        .last("LIMIT 1" + (lock ? " FOR UPDATE" : "")));
    }

    private static void validateIdempotent(
            InspectionEvidence existing, InspectionEvidenceCommands.Authorize input) {
        if (!existing.getRunId().equals(input.runId())
                || !existing.getAssignmentEpoch().equals(input.assignmentEpoch())
                || !existing.getRequestId().equals(input.requestId())
                || !existing.getKind().equals(input.kind())
                || existing.getSizeBytes() != input.sizeBytes()
                || !existing.getContentType().equals(input.contentType())
                || !existing.getSha256().equals(hex(input.checksumBase64())))
            throw conflict("EVIDENCE_IDEMPOTENCY_CONFLICT");
    }

    private InspectionEvidence placement(String id, int revision, boolean lock) {
        return evidence.selectOne(
                new LambdaQueryWrapper<InspectionEvidence>()
                        .eq(InspectionEvidence::getEvidenceId, id)
                        .eq(InspectionEvidence::getPlacementRevision, revision)
                        .last(lock ? "FOR UPDATE" : ""));
    }

    private long accounted(long scopeId) {
        Object value =
                evidence.selectObjs(
                                new QueryWrapper<InspectionEvidence>()
                                        .select("COALESCE(SUM(size_bytes),0)")
                                        .eq("scope_id", scopeId))
                        .getFirst();
        return ((Number) value).longValue();
    }

    private boolean sameBytes(
            StorageInstance instance, InspectionEvidence row, StorageS3Client.ObjectInfo object) {
        try (var response = s3.content(instance, row.getBucket(), object, null)) {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            InputStream stream = response.body();
            byte[] buffer = new byte[65536];
            long count = 0;
            for (int n; (n = stream.read(buffer)) != -1; ) {
                count += n;
                if (count > MAX_OBJECT_BYTES || count > row.getSizeBytes()) return false;
                digest.update(buffer, 0, n);
            }
            return count == row.getSizeBytes()
                    && MessageDigest.isEqual(
                            digest.digest(), HexFormat.of().parseHex(row.getSha256()));
        } catch (Exception ex) {
            return false;
        }
    }

    private static void validate(InspectionEvidenceCommands.Authorize input) {
        if (input == null
                || !identifier(input.requestId())
                || !identifier(input.evidenceId())
                || !identifier(input.runId())
                || !identifier(input.traceId())
                || input.assignmentEpoch() == null
                || input.assignmentEpoch() < 1
                || input.sizeBytes() == null
                || input.sizeBytes() < 1
                || input.sizeBytes() > MAX_OBJECT_BYTES
                || input.contentType() == null
                || input.contentType().length() > 128
                || !("IMAGE".equals(input.kind()) && IMAGES.contains(input.contentType())
                        || "VIDEO".equals(input.kind()) && VIDEOS.contains(input.contentType())))
            throw invalid();
        hex(input.checksumBase64());
    }

    private static String hex(String base64) {
        if (base64 == null) throw invalid();
        try {
            byte[] value = Base64.getDecoder().decode(base64);
            if (value.length != 32 || !Base64.getEncoder().encodeToString(value).equals(base64))
                throw invalid();
            return HexFormat.of().formatHex(value);
        } catch (IllegalArgumentException ex) {
            throw invalid();
        }
    }

    private static boolean identifier(String value) {
        return value != null && value.matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,63}");
    }

    private static long numeric(String value) {
        try {
            return DecimalInput.id(value, "id");
        } catch (RuntimeException ex) {
            throw conflict("EVIDENCE_SPEC_ID_INVALID");
        }
    }

    private static LocalDateTime utc(Instant instant) {
        return LocalDateTime.ofInstant(instant, ZoneOffset.UTC);
    }

    private static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }

    private static BusinessException conflict(String reason) {
        return BusinessException.error(ErrorCode.CONFLICT).diagnosed(reason);
    }

    private static BusinessException unavailable(String reason) {
        return BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE).diagnosed(reason);
    }
}
