package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.incrementer.DefaultIdentifierGenerator;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRequestKey;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.diagnostics.DiagnosticLog;
import com.streamfusion.platform.user.service.UserService;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.temporal.ChronoUnit;
import java.util.Map;
import java.util.Objects;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Supplier;
import lombok.RequiredArgsConstructor;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.security.core.context.SecurityContext;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.session.Session;
import org.springframework.session.SessionRepository;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;

/** Manual control checks. No user session secret or plaintext connection is stored. */
@Service
@RequiredArgsConstructor
public class CameraStatusService {
    private static final Logger LOG = LoggerFactory.getLogger(CameraStatusService.class);
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private static final String PROBE = "RTSP_DESCRIBE";
    private final CameraStatusCheckMapper checks;
    private final CameraStatusSnapshotMapper snapshots;
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraAccessService cameras;
    private final CameraCryptoService crypto;
    private final CameraRtspTargetService targets;
    private final CameraStatusWorker worker;
    private final CameraStatusProperties properties;
    private final CurrentUserService current;
    private final UserService users;
    private final AccessService access;
    private final AccessMapper coordination;
    private final ObjectProvider<SessionRepository<? extends Session>> repositories;
    private final PlatformTransactionManager manager;
    private final Clock clock;
    private final CameraStatusPlanMapper plans;
    private final DiagnosticLog diagnostics;
    private final Map<Long, Execution> submitted = new ConcurrentHashMap<>();

    public CameraStatusCheckVo createManual(
            String cameraId, CameraStatusCheckCommand command, String sessionId) {
        if (command == null || sessionId == null || sessionId.isEmpty())
            throw error(ErrorCode.VALIDATION_ERROR);
        long id = DecimalInput.id(cameraId, "cameraId");
        long profileId = DecimalInput.id(command.streamProfileId(), "streamProfileId");
        Instant requested = CameraRequestKey.timestamp(command.clientRequestId());
        return transaction(
                () -> {
                    var actor = cameras.lockActor();
                    cameras.requireVisible(id, actor);
                    var camera = lockCamera(id);
                    if (camera == null) throw error(ErrorCode.NOT_FOUND);
                    var input = requestInput(id, profileId);
                    var existing = checks.byRequest(actor.userId(), command.clientRequestId());
                    if (existing != null) {
                        if (!MessageDigest.isEqual(
                                existing.getRequestFingerprint(),
                                crypto.digest(
                                        existing.getRequestKeyId(),
                                        "camera-status-request",
                                        input))) throw error(ErrorCode.CONFLICT);
                        return CameraStatusCheckVo.from(existing);
                    }
                    CameraRequestKey.requireFresh(requested, nowInstant());
                    var profile = lockProfile(id, profileId);
                    if (profile == null) throw error(ErrorCode.NOT_FOUND);
                    String keyId = crypto.activeKeyId();
                    var target = targets.inspect(camera, profile, keyId, () -> {});
                    if (target.blockingReasonCode() != null)
                        throw error(ErrorCode.CONFLICT).diagnosed(target.blockingReasonCode());
                    long pending =
                            checks.selectCount(
                                    new LambdaQueryWrapper<CameraStatusCheckEntity>()
                                            .in(
                                                    CameraStatusCheckEntity::getExecutionState,
                                                    "QUEUED",
                                                    "RUNNING")
                                            .gt(CameraStatusCheckEntity::getDeadlineAt, now()));
                    long recent =
                            checks.selectCount(
                                    new LambdaQueryWrapper<CameraStatusCheckEntity>()
                                            .eq(
                                                    CameraStatusCheckEntity::getActorUserId,
                                                    actor.userId())
                                            .isNull(CameraStatusCheckEntity::getPlanId)
                                            .ge(
                                                    CameraStatusCheckEntity::getRequestedAt,
                                                    now().minusMinutes(1)));
                    if (pending >= properties.getWorkerThreads() + properties.getQueueCapacity()
                            || recent >= 20) throw error(ErrorCode.RATE_LIMITED);
                    var check = new CameraStatusCheckEntity();
                    check.setId(DefaultIdentifierGenerator.getInstance().nextId(check).longValue());
                    check.setActorUserId(actor.userId());
                    check.setCameraId(id);
                    check.setProfileId(profileId);
                    check.setSourceId(profile.getSourceId());
                    check.setClientRequestId(command.clientRequestId());
                    check.setRequestKeyId(keyId);
                    check.setRequestFingerprint(
                            crypto.digest(keyId, "camera-status-request", input));
                    check.setSessionKeyId(keyId);
                    check.setSessionDigest(
                            crypto.digest(keyId, "camera-status-session", sessionId));
                    check.setConnectionKeyId(keyId);
                    check.setConnectionDigest(target.connectionDigest());
                    check.setProbeKind(PROBE);
                    check.setExecutionState("QUEUED");
                    String trace = MDC.get("traceId");
                    check.setTraceId(
                            trace != null && trace.matches("[a-f0-9]{32}")
                                    ? trace
                                    : UUID.randomUUID().toString().replace("-", ""));
                    check.setRequestedAt(now());
                    check.setUpdatedAt(check.getRequestedAt());
                    check.setDeadlineAt(
                            check.getRequestedAt()
                                    .plusNanos(properties.getExecutionTimeoutMs() * 1_000_000));
                    check.setRetainUntil(
                            check.getRequestedAt().plusDays(properties.getRetentionDays()));
                    check.setVersion(0L);
                    check.setSnapshotApplied(false);
                    checks.insert(check);
                    logCheck(check, "QUEUED", null);
                    return CameraStatusCheckVo.from(check);
                });
    }

    /** Called after the check transaction commits; no DNS or protocol exchange here. */
    public void dispatch(long id, String sessionId) {
        var check = checks.selectById(id);
        if (check == null
                || !"QUEUED".equals(check.getExecutionState())
                || check.getPlanId() == null && !sameSession(check, sessionId)) return;
        var execution = new Execution(sessionId);
        if (submitted.putIfAbsent(id, execution) != null) return;
        try {
            long remaining =
                    Duration.between(nowInstant(), instant(check.getDeadlineAt())).toMillis();
            if (remaining <= 0) {
                skipQueued(id, "CHECK_EXPIRED");
                submitted.remove(id, execution);
                return;
            }
            var inspection =
                    targets.inspect(
                            channels.selectById(check.getCameraId()),
                            profiles.selectById(check.getProfileId()),
                            check.getConnectionKeyId(),
                            () -> checkWork(id, execution.claim.get(), sessionId));
            CameraRtspTargetService.requireRunnable(inspection);
            var submission =
                    worker.trySubmit(
                            new CameraStatusWorker.Request(
                                    id,
                                    check.getSourceId(),
                                    check.getProfileId(),
                                    check.getTraceId(),
                                    inspection.target(),
                                    Duration.ofMillis(
                                            Math.min(properties.getProbeTimeoutMs(), remaining)),
                                    () -> execution.claim.set(claim(id, sessionId)),
                                    inspection.targetResolver()));
            submission
                    .completion()
                    .whenComplete(
                            (result, failure) -> {
                                try {
                                    complete(
                                            id,
                                            execution.claim.get(),
                                            sessionId,
                                            failure == null && result != null
                                                    ? result
                                                    : new RtspDescribeProbe.Result(
                                                            RtspDescribeProbe.Outcome.NOT_EXECUTED,
                                                            "PROBE_UNAVAILABLE",
                                                            0,
                                                            0));
                                } catch (RuntimeException ex) {
                                    LOG.warn(
                                            "Camera status completion unavailable traceId={} checkId={}",
                                            check.getTraceId(),
                                            id);
                                } finally {
                                    submitted.remove(id, execution);
                                }
                            });
        } catch (CameraAdapterException ex) {
            submitted.remove(id, execution);
            skipQueued(id, ex.reasonCode());
        } catch (RuntimeException ex) {
            submitted.remove(id, execution);
            skipQueued(id, "CONFIGURATION_UNAVAILABLE");
        }
    }

    public String claim(long id, String sessionId) {
        String token =
                transaction(
                        () -> {
                            coordination.lockSuperAdminRole();
                            var prior = checks.selectById(id);
                            if (prior == null) return null;
                            if (prior.getPlanId() != null) plans.lock(prior.getPlanId());
                            var camera = lockCamera(prior.getCameraId());
                            var profile = lockProfile(prior.getCameraId(), prior.getProfileId());
                            var check = checks.lock(id);
                            if (check == null || !"QUEUED".equals(check.getExecutionState()))
                                return null;
                            String reason = invalidReason(check, camera, profile, sessionId);
                            if (reason != null) {
                                finish(
                                        check,
                                        "ACTOR_REVOKED".equals(reason) ? "CANCELLED" : "SKIPPED",
                                        reason,
                                        null);
                                return null;
                            }
                            String claim = UUID.randomUUID().toString();
                            int changed =
                                    checks.update(
                                            null,
                                            new LambdaUpdateWrapper<CameraStatusCheckEntity>()
                                                    .eq(CameraStatusCheckEntity::getId, id)
                                                    .eq(
                                                            CameraStatusCheckEntity
                                                                    ::getExecutionState,
                                                            "QUEUED")
                                                    .eq(
                                                            CameraStatusCheckEntity::getVersion,
                                                            check.getVersion())
                                                    .set(
                                                            CameraStatusCheckEntity
                                                                    ::getExecutionState,
                                                            "RUNNING")
                                                    .set(
                                                            CameraStatusCheckEntity::getClaimToken,
                                                            claim)
                                                    .set(
                                                            CameraStatusCheckEntity::getStartedAt,
                                                            now())
                                                    .set(
                                                            CameraStatusCheckEntity::getUpdatedAt,
                                                            now())
                                                    .setIncrBy(
                                                            CameraStatusCheckEntity::getVersion,
                                                            1));
                            if (changed == 1) logCheck(check, "RUNNING", null);
                            return changed == 1 ? claim : null;
                        });
        if (token == null) throw new CameraAdapterException("CHECK_NO_LONGER_ACTIVE");
        return token;
    }

    public void checkWork(long id, String claim, String sessionId) {
        var check = checks.selectById(id);
        if (check == null
                || claim == null
                || !"RUNNING".equals(check.getExecutionState())
                || !claim.equals(check.getClaimToken()))
            throw new CameraAdapterException("CHECK_NO_LONGER_ACTIVE");
        String reason =
                invalidReason(
                        check,
                        channels.selectById(check.getCameraId()),
                        profiles.selectById(check.getProfileId()),
                        sessionId);
        if (reason != null) throw new CameraAdapterException(reason);
    }

    public void complete(long id, String claim, String sessionId, RtspDescribeProbe.Result result) {
        transaction(
                () -> {
                    coordination.lockSuperAdminRole();
                    var prior = checks.selectById(id);
                    if (prior == null) return null;
                    if (prior.getPlanId() != null) plans.lock(prior.getPlanId());
                    var camera = lockCamera(prior.getCameraId());
                    var profile = lockProfile(prior.getCameraId(), prior.getProfileId());
                    var check = checks.lock(id);
                    if (check == null) return null;
                    if (claim == null) {
                        if ("QUEUED".equals(check.getExecutionState()))
                            finish(check, "SKIPPED", safeReason(result.reasonCode()), result);
                        return null;
                    }
                    if (!"RUNNING".equals(check.getExecutionState())
                            || !claim.equals(check.getClaimToken())) return null;
                    String reason = invalidReason(check, camera, profile, sessionId);
                    if (reason != null) {
                        finish(
                                check,
                                "ACTOR_REVOKED".equals(reason)
                                        ? "CANCELLED"
                                        : "TARGET_DELETED".equals(reason)
                                                ? "INTERRUPTED"
                                                : "SKIPPED",
                                reason,
                                result);
                        return null;
                    }
                    if (result.outcome() == RtspDescribeProbe.Outcome.CANCELLED) {
                        finish(check, "CANCELLED", safeReason(result.reasonCode()), result);
                    } else if (result.outcome() == RtspDescribeProbe.Outcome.NOT_EXECUTED) {
                        finish(check, "SKIPPED", safeReason(result.reasonCode()), result);
                    } else {
                        check.setResultValue(result.outcome().name());
                        check.setObservedAt(now());
                        check.setReadAt(check.getObservedAt());
                        check.setValidUntil(
                                check.getObservedAt()
                                        .plusNanos(properties.getFreshnessMs() * 1_000_000));
                        var previous = snapshots.selectById(check.getProfileId());
                        boolean applies =
                                previous == null
                                        || previous.getCheckRequestedAt()
                                                .isBefore(check.getRequestedAt())
                                        || previous.getCheckRequestedAt()
                                                        .equals(check.getRequestedAt())
                                                && previous.getCheckId() < check.getId();
                        check.setSnapshotApplied(applies);
                        if (finish(check, "COMPLETED", safeReason(result.reasonCode()), result)
                                && applies) {
                            var snapshot = new CameraStatusSnapshotEntity();
                            snapshot.setProfileId(check.getProfileId());
                            snapshot.setCameraId(check.getCameraId());
                            snapshot.setLayer("CONTROL");
                            snapshot.setProbeKind(PROBE);
                            snapshot.setValue(check.getResultValue());
                            snapshot.setReasonCode(check.getReasonCode());
                            snapshot.setObservedAt(check.getObservedAt());
                            snapshot.setReadAt(check.getReadAt());
                            snapshot.setValidUntil(check.getValidUntil());
                            snapshot.setCheckRequestedAt(check.getRequestedAt());
                            snapshot.setCheckId(check.getId());
                            snapshot.setConnectionKeyId(check.getConnectionKeyId());
                            snapshot.setConnectionDigest(check.getConnectionDigest());
                            recordTimeline(snapshot, previous, camera, profile);
                            snapshot.setUpdatedAt(now());
                            if (previous == null) snapshots.insert(snapshot);
                            else snapshots.updateById(snapshot);
                        }
                    }
                    return null;
                });
    }

    private void recordTimeline(
            CameraStatusSnapshotEntity next,
            CameraStatusSnapshotEntity previous,
            CameraChannelEntity camera,
            CameraProfileEntity profile) {
        boolean sameConnection = false;
        if (previous != null) {
            var original =
                    targets.inspect(camera, profile, previous.getConnectionKeyId(), () -> {});
            sameConnection =
                    original.connectionDigest() != null
                            && MessageDigest.isEqual(
                                    previous.getConnectionDigest(), original.connectionDigest());
        }
        var prior = sameConnection ? CameraStatusTimelineVo.from(previous) : null;
        next.setStateSince(
                sameConnection && Objects.equals(previous.getValue(), next.getValue())
                        ? LocalDateTime.ofInstant(prior.stateSince(), ZONE)
                        : next.getObservedAt());
        next.setLastAccessibleAt(
                "ACCESSIBLE".equals(next.getValue())
                        ? next.getObservedAt()
                        : prior == null || prior.lastAccessibleAt() == null
                                ? null
                                : LocalDateTime.ofInstant(prior.lastAccessibleAt(), ZONE));
        next.setLastFailedAt(
                "FAILED".equals(next.getValue())
                        ? next.getObservedAt()
                        : prior == null || prior.lastFailedAt() == null
                                ? null
                                : LocalDateTime.ofInstant(prior.lastFailedAt(), ZONE));
    }

    private String invalidReason(
            CameraStatusCheckEntity check,
            CameraChannelEntity camera,
            CameraProfileEntity profile,
            String sessionId) {
        if (camera == null
                || profile == null
                || !Objects.equals(profile.getChannelId(), check.getCameraId()))
            return "TARGET_DELETED";
        if (!nowInstant().isBefore(instant(check.getDeadlineAt()))) return "CHECK_EXPIRED";
        try {
            if (check.getPlanId() != null && !scheduledAllowed(check))
                return "PLAN_NO_LONGER_ACTIVE";
            if (check.getPlanId() == null
                    && (!sameSession(check, sessionId) || !sessionAllowed(check, sessionId)))
                return "ACTOR_REVOKED";
            var inspected = targets.inspect(camera, profile, check.getConnectionKeyId(), () -> {});
            if ("CONFIGURATION_UNAVAILABLE".equals(inspected.blockingReasonCode()))
                return "CONFIGURATION_UNAVAILABLE";
            if (inspected.blockingReasonCode() != null
                    || inspected.connectionDigest() == null
                    || !MessageDigest.isEqual(
                            check.getConnectionDigest(), inspected.connectionDigest()))
                return "CONFIGURATION_CHANGED";
            return null;
        } catch (BusinessException ex) {
            return "CONFIGURATION_UNAVAILABLE";
        } catch (CameraAdapterException ex) {
            return "SESSION_STORE_UNAVAILABLE".equals(ex.reasonCode())
                    ? "SESSION_STORE_UNAVAILABLE"
                    : "AUTHORIZATION_UNAVAILABLE";
        }
    }

    private boolean sessionAllowed(CameraStatusCheckEntity check, String sessionId) {
        Session session;
        try {
            var repository = repositories.getIfAvailable();
            if (repository == null) throw new CameraAdapterException("SESSION_STORE_UNAVAILABLE");
            session = repository.findById(sessionId);
        } catch (RuntimeException ex) {
            throw new CameraAdapterException("SESSION_STORE_UNAVAILABLE");
        }
        if (session == null || session.isExpired()) return false;
        Object stored =
                session.getAttribute(
                        HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY);
        if (!(stored instanceof SecurityContext context)
                || context.getAuthentication() == null
                || !context.getAuthentication().isAuthenticated()
                || !(context.getAuthentication().getPrincipal()
                        instanceof SessionPrincipalDto principal)
                || principal.getUserId() != check.getActorUserId()) return false;
        try {
            var user = users.getById(check.getActorUserId());
            current.validate(principal, user);
            if (CurrentUserService.requiresPasswordChange(user)
                    || !current.hasModuleAccess(check.getActorUserId(), "camera")) return false;
            cameras.requireVisible(
                    check.getCameraId(),
                    new CameraAccessService.Actor(
                            check.getActorUserId(), access.isSuperAdmin(check.getActorUserId())));
            return true;
        } catch (BusinessException ex) {
            return false;
        }
    }

    boolean planActorAllowed(long userId, long cameraId) {
        var user = users.getById(userId);
        if (user == null
                || user.getStatus() != 1
                || CurrentUserService.requiresPasswordChange(user)
                || !current.hasModuleAccess(userId, "camera")) return false;
        try {
            cameras.requireVisible(
                    cameraId, new CameraAccessService.Actor(userId, access.isSuperAdmin(userId)));
            return true;
        } catch (BusinessException ex) {
            return false;
        }
    }

    private boolean scheduledAllowed(CameraStatusCheckEntity check) {
        var plan = plans.selectById(check.getPlanId());
        return plan != null
                && "ACTIVE".equals(plan.getState())
                && Objects.equals(plan.getActorUserId(), check.getActorUserId())
                && !now().isBefore(plan.getStartAt())
                && now().isBefore(plan.getEndAt())
                && planActorAllowed(check.getActorUserId(), check.getCameraId());
    }

    /** Called inside the scheduler's locked plan/target transaction; no session or network work. */
    CameraStatusCheckVo createScheduled(
            CameraStatusPlanEntity plan, CameraStatusPlanTargetEntity target, LocalDateTime slot) {
        var camera = lockCamera(target.getCameraId());
        var profile = lockProfile(target.getCameraId(), target.getProfileId());
        var inspected = targets.inspect(camera, profile, target.getConnectionKeyId(), () -> {});
        if (inspected.blockingReasonCode() != null
                || inspected.connectionDigest() == null
                || !MessageDigest.isEqual(
                        target.getConnectionDigest(), inspected.connectionDigest()))
            throw new CameraAdapterException("CONFIGURATION_CHANGED");
        long pending =
                checks.selectCount(
                        new LambdaQueryWrapper<CameraStatusCheckEntity>()
                                .in(CameraStatusCheckEntity::getExecutionState, "QUEUED", "RUNNING")
                                .gt(CameraStatusCheckEntity::getDeadlineAt, now()));
        if (pending >= properties.getWorkerThreads() + properties.getQueueCapacity())
            throw new CameraAdapterException("WORKER_BUSY");
        String keyId = crypto.activeKeyId();
        var check = new CameraStatusCheckEntity();
        check.setId(DefaultIdentifierGenerator.getInstance().nextId(check).longValue());
        check.setPlanId(plan.getId());
        check.setActorUserId(plan.getActorUserId());
        check.setCameraId(target.getCameraId());
        check.setProfileId(target.getProfileId());
        check.setSourceId(profile.getSourceId());
        check.setClientRequestId(
                nowInstant().toEpochMilli()
                        + "-"
                        + UUID.nameUUIDFromBytes(
                                (plan.getId() + "/" + target.getId() + "/" + slot)
                                        .getBytes(java.nio.charset.StandardCharsets.UTF_8)));
        check.setRequestKeyId(keyId);
        check.setRequestFingerprint(
                crypto.digest(
                        keyId,
                        "camera-status-request",
                        requestInput(check.getCameraId(), check.getProfileId())));
        check.setSessionKeyId(keyId);
        check.setSessionDigest(crypto.digest(keyId, "camera-status-plan", plan.getId().toString()));
        check.setConnectionKeyId(target.getConnectionKeyId());
        check.setConnectionDigest(target.getConnectionDigest());
        check.setProbeKind(PROBE);
        check.setExecutionState("QUEUED");
        check.setTraceId(UUID.randomUUID().toString().replace("-", ""));
        check.setRequestedAt(now());
        check.setUpdatedAt(check.getRequestedAt());
        check.setDeadlineAt(now().plusNanos(properties.getExecutionTimeoutMs() * 1_000_000));
        check.setRetainUntil(now().plusDays(properties.getRetentionDays()));
        check.setSnapshotApplied(false);
        check.setVersion(0L);
        checks.insert(check);
        logCheck(check, "QUEUED", null);
        return CameraStatusCheckVo.from(check);
    }

    private boolean sameSession(CameraStatusCheckEntity check, String sessionId) {
        if (sessionId == null) return false;
        try {
            return MessageDigest.isEqual(
                    check.getSessionDigest(),
                    crypto.digest(check.getSessionKeyId(), "camera-status-session", sessionId));
        } catch (BusinessException ex) {
            return false;
        }
    }

    private void skipQueued(long id, String reason) {
        transaction(
                () -> {
                    coordination.lockSuperAdminRole();
                    var check = checks.lock(id);
                    if (check != null && "QUEUED".equals(check.getExecutionState()))
                        finish(check, "SKIPPED", safeReason(reason), null);
                    return null;
                });
    }

    private boolean finish(
            CameraStatusCheckEntity check,
            String state,
            String reason,
            RtspDescribeProbe.Result result) {
        check.setReasonCode(reason);
        LocalDateTime finished = now();
        boolean updated =
                checks.update(
                                null,
                                new LambdaUpdateWrapper<CameraStatusCheckEntity>()
                                        .eq(CameraStatusCheckEntity::getId, check.getId())
                                        .eq(
                                                CameraStatusCheckEntity::getExecutionState,
                                                check.getExecutionState())
                                        .eq(CameraStatusCheckEntity::getVersion, check.getVersion())
                                        .set(CameraStatusCheckEntity::getExecutionState, state)
                                        .set(CameraStatusCheckEntity::getReasonCode, reason)
                                        .set(CameraStatusCheckEntity::getClaimToken, null)
                                        .set(CameraStatusCheckEntity::getFinishedAt, finished)
                                        .set(CameraStatusCheckEntity::getUpdatedAt, finished)
                                        .set(
                                                CameraStatusCheckEntity::getDurationMs,
                                                result == null
                                                        ? null
                                                        : Math.max(0, result.durationMs()))
                                        .set(
                                                CameraStatusCheckEntity::getResultValue,
                                                check.getResultValue())
                                        .set(
                                                CameraStatusCheckEntity::getObservedAt,
                                                check.getObservedAt())
                                        .set(CameraStatusCheckEntity::getReadAt, check.getReadAt())
                                        .set(
                                                CameraStatusCheckEntity::getValidUntil,
                                                check.getValidUntil())
                                        .set(
                                                CameraStatusCheckEntity::getSnapshotApplied,
                                                Boolean.TRUE.equals(check.getSnapshotApplied()))
                                        .setIncrBy(CameraStatusCheckEntity::getVersion, 1))
                        == 1;
        if (updated) {
            LOG.info(
                    "Camera status check ended traceId={} checkId={} state={} reasonCode={}",
                    check.getTraceId(),
                    check.getId(),
                    state,
                    reason);
            logCheck(check, state, result == null ? null : Math.max(0, result.durationMs()));
        }
        return updated;
    }

    private void logCheck(CameraStatusCheckEntity check, String state, Long duration) {
        diagnostics.check(
                check.getTraceId(),
                check.getPlanId(),
                check.getId(),
                check.getCameraId(),
                check.getProfileId(),
                state,
                check.getResultValue(),
                check.getReasonCode(),
                duration);
    }

    public void recoverInterruptedOnStartup() {
        LocalDateTime cutoff = now();
        int recovered;
        do {
            recovered =
                    transaction(
                            () -> {
                                coordination.lockSuperAdminRole();
                                var ids = checks.activeBefore(cutoff);
                                if (ids.isEmpty()) return 0;
                                checks.update(
                                        null,
                                        new LambdaUpdateWrapper<CameraStatusCheckEntity>()
                                                .in(CameraStatusCheckEntity::getId, ids)
                                                .in(
                                                        CameraStatusCheckEntity::getExecutionState,
                                                        "QUEUED",
                                                        "RUNNING")
                                                .set(
                                                        CameraStatusCheckEntity::getExecutionState,
                                                        "INTERRUPTED")
                                                .set(
                                                        CameraStatusCheckEntity::getReasonCode,
                                                        "SERVICE_RESTARTED")
                                                .set(CameraStatusCheckEntity::getFinishedAt, now())
                                                .set(CameraStatusCheckEntity::getUpdatedAt, now())
                                                .set(CameraStatusCheckEntity::getClaimToken, null)
                                                .setIncrBy(CameraStatusCheckEntity::getVersion, 1));
                                return ids.size();
                            });
        } while (recovered == 100);
    }

    /** Small batches; no historical checks are replayed, and no state GET triggers maintenance. */
    public void maintenance() {
        for (var entry : submitted.entrySet()) {
            String claim = entry.getValue().claim.get();
            if (claim != null) {
                try {
                    checkWork(entry.getKey(), claim, entry.getValue().sessionId);
                } catch (RuntimeException ex) {
                    worker.cancel(entry.getKey());
                }
            }
        }
        for (long id : checks.expired(now())) {
            worker.cancel(id);
            transaction(
                    () -> {
                        coordination.lockSuperAdminRole();
                        var check = checks.lock(id);
                        if (check != null
                                && ("QUEUED".equals(check.getExecutionState())
                                        || "RUNNING".equals(check.getExecutionState()))
                                && !now().isBefore(check.getDeadlineAt()))
                            finish(check, "SKIPPED", "CHECK_EXPIRED", null);
                        return null;
                    });
        }
        var ids = checks.prunable(now());
        if (!ids.isEmpty())
            checks.delete(
                    new LambdaQueryWrapper<CameraStatusCheckEntity>()
                            .in(CameraStatusCheckEntity::getId, ids)
                            .notIn(CameraStatusCheckEntity::getExecutionState, "QUEUED", "RUNNING")
                            .le(CameraStatusCheckEntity::getRetainUntil, now()));
    }

    private CameraChannelEntity lockCamera(long id) {
        return channels.selectOne(
                new LambdaQueryWrapper<CameraChannelEntity>()
                        .eq(CameraChannelEntity::getId, id)
                        .last("FOR UPDATE"));
    }

    private CameraProfileEntity lockProfile(long cameraId, long profileId) {
        return profiles.selectOne(
                new LambdaQueryWrapper<CameraProfileEntity>()
                        .eq(CameraProfileEntity::getId, profileId)
                        .eq(CameraProfileEntity::getChannelId, cameraId)
                        .last("FOR UPDATE"));
    }

    private <T> T transaction(Supplier<T> action) {
        var template = new TransactionTemplate(manager);
        template.setIsolationLevel(TransactionDefinition.ISOLATION_READ_COMMITTED);
        return template.execute(status -> action.get());
    }

    private static Map<String, String> requestInput(long cameraId, long profileId) {
        return Map.of(
                "cameraId", Long.toString(cameraId), "streamProfileId", Long.toString(profileId));
    }

    private Instant nowInstant() {
        return clock.instant().truncatedTo(ChronoUnit.MILLIS);
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(nowInstant(), ZONE);
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZONE).toInstant();
    }

    private static BusinessException error(ErrorCode code) {
        return BusinessException.error(code);
    }

    private static String safeReason(String reason) {
        return reason != null && reason.matches("[A-Z0-9_]{1,64}") ? reason : "PROBE_UNAVAILABLE";
    }

    private static final class Execution {
        private final String sessionId;
        private final AtomicReference<String> claim = new AtomicReference<>();

        private Execution(String sessionId) {
            this.sessionId = sessionId;
        }
    }
}
