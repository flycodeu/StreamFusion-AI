package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.service.UserService;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.temporal.ChronoUnit;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.security.core.context.SecurityContext;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.session.Session;
import org.springframework.session.SessionRepository;
import org.springframework.stereotype.Component;

/** Rechecks the original actor, execution window and connection without opening a transaction. */
@Component
@RequiredArgsConstructor
public class CameraStatusExecutionGuard {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraAccessService cameras;
    private final CameraCryptoService crypto;
    private final CameraRtspTargetService targets;
    private final CurrentUserService current;
    private final UserService users;
    private final AccessService access;
    private final ObjectProvider<SessionRepository<? extends Session>> repositories;
    private final CameraStatusPlanMapper plans;
    private final Clock clock;

    String invalidReason(
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

    boolean scheduledAllowed(CameraStatusCheckEntity check) {
        var plan = plans.selectById(check.getPlanId());
        return plan != null
                && "ACTIVE".equals(plan.getState())
                && Objects.equals(plan.getActorUserId(), check.getActorUserId())
                && MessageDigest.isEqual(
                        check.getSessionDigest(), planDigest(check.getSessionKeyId(), plan))
                && !now().isBefore(plan.getStartAt())
                && now().isBefore(plan.getEndAt())
                && planActorAllowed(check.getActorUserId(), check.getCameraId());
    }

    byte[] planDigest(String keyId, CameraStatusPlanEntity plan) {
        return crypto.digest(keyId, "camera-status-plan", plan.getId() + "/" + plan.getVersion());
    }

    boolean sameSession(CameraStatusCheckEntity check, String sessionId) {
        if (sessionId == null) return false;
        try {
            return MessageDigest.isEqual(
                    check.getSessionDigest(),
                    crypto.digest(check.getSessionKeyId(), "camera-status-session", sessionId));
        } catch (BusinessException ex) {
            return false;
        }
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
}
