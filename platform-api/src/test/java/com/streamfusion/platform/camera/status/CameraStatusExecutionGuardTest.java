package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.pojo.entity.UserEntity;
import com.streamfusion.platform.system.user.service.UserService;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.context.SecurityContextImpl;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.session.Session;
import org.springframework.session.SessionRepository;

class CameraStatusExecutionGuardTest {
    private static final Instant NOW = Instant.parse("2026-10-17T02:00:00Z");
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private static final byte[] DIGEST = {1, 2, 3};
    private final CameraAccessService cameras = mock(CameraAccessService.class);
    private final CameraCryptoService crypto = mock(CameraCryptoService.class);
    private final CameraRtspTargetService targets = mock(CameraRtspTargetService.class);
    private final CurrentUserService current = mock(CurrentUserService.class);
    private final UserService users = mock(UserService.class);
    private final AccessService access = mock(AccessService.class);
    private final CameraStatusPlanMapper plans = mock(CameraStatusPlanMapper.class);

    @SuppressWarnings("unchecked")
    private final ObjectProvider<SessionRepository<? extends Session>> repositories =
            mock(ObjectProvider.class);

    private final CameraStatusExecutionGuard guard =
            new CameraStatusExecutionGuard(
                    cameras,
                    crypto,
                    targets,
                    current,
                    users,
                    access,
                    repositories,
                    plans,
                    Clock.fixed(NOW, ZoneOffset.UTC));

    @Test
    void deletedTargetAndExactDeadlineRejectBeforeSessionOrConnectionWork() {
        var check = check();
        var camera = camera();
        var profile = profile();
        assertThat(guard.invalidReason(check, null, profile, "original"))
                .isEqualTo("TARGET_DELETED");
        profile.setChannelId(99L);
        assertThat(guard.invalidReason(check, camera, profile, "original"))
                .isEqualTo("TARGET_DELETED");
        profile.setChannelId(check.getCameraId());
        check.setDeadlineAt(local(NOW));
        assertThat(guard.invalidReason(check, camera, profile, "original"))
                .isEqualTo("CHECK_EXPIRED");
        verifyNoInteractions(crypto, repositories, targets, plans, current, users, cameras);
    }

    @Test
    void originalSessionIsRequiredAndStoreFailureIsNotMisreportedAsActorRevocation() {
        var check = check();
        when(crypto.digest("key", "camera-status-session", "other")).thenReturn(new byte[] {9});
        assertThat(guard.invalidReason(check, camera(), profile(), "other"))
                .isEqualTo("ACTOR_REVOKED");
        verifyNoInteractions(repositories, targets);
        when(crypto.digest("key", "camera-status-session", "original")).thenReturn(DIGEST);
        assertThat(guard.invalidReason(check, camera(), profile(), "original"))
                .isEqualTo("SESSION_STORE_UNAVAILABLE");
        when(repositories.getIfAvailable()).thenThrow(new IllegalStateException("unavailable"));
        assertThat(guard.invalidReason(check, camera(), profile(), "original"))
                .isEqualTo("SESSION_STORE_UNAVAILABLE");
        verifyNoInteractions(targets);
    }

    @Test
    @SuppressWarnings("unchecked")
    void revokedManualScopeIsCheckedBeforeConnectionAndValidSessionUsesCurrentDigest() {
        var check = check();
        var camera = camera();
        var profile = profile();
        var repository = (SessionRepository<Session>) mock(SessionRepository.class);
        var session = mock(Session.class);
        var principal = new SessionPrincipalDto(1L, 2L, NOW.minusSeconds(60));
        var authentication =
                UsernamePasswordAuthenticationToken.authenticated(principal, null, List.of());
        when(session.getAttribute(HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY))
                .thenReturn(new SecurityContextImpl(authentication));
        when(repository.findById("original")).thenReturn(session);
        doReturn(repository).when(repositories).getIfAvailable();
        when(crypto.digest("key", "camera-status-session", "original")).thenReturn(DIGEST);
        allowUser();
        doThrow(BusinessException.error(ErrorCode.NOT_FOUND))
                .when(cameras)
                .requireVisible(eq(2L), any(CameraAccessService.Actor.class));
        assertThat(guard.invalidReason(check, camera, profile, "original"))
                .isEqualTo("ACTOR_REVOKED");
        verifyNoInteractions(targets);
        doNothing().when(cameras).requireVisible(eq(2L), any(CameraAccessService.Actor.class));
        when(targets.inspect(eq(camera), eq(profile), eq("key"), any()))
                .thenReturn(new CameraRtspTargetService.Inspection(null, DIGEST, null));
        assertThat(guard.invalidReason(check, camera, profile, "original")).isNull();
        verify(current, times(2)).validate(eq(principal), any(UserEntity.class));
    }

    @Test
    void scheduledWorkRequiresLivePlanVersionWindowAndActorWithoutSessionStore() {
        var check = check();
        check.setPlanId(9L);
        var plan = new CameraStatusPlanEntity();
        plan.setId(9L);
        plan.setVersion(3L);
        plan.setActorUserId(1L);
        plan.setState("ACTIVE");
        plan.setStartAt(local(NOW.minusSeconds(30)));
        plan.setEndAt(local(NOW.plusSeconds(30)));
        when(plans.selectById(9L)).thenReturn(plan);
        when(crypto.digest("key", "camera-status-plan", "9/3")).thenReturn(DIGEST);
        allowUser();
        var camera = camera();
        var profile = profile();
        when(targets.inspect(eq(camera), eq(profile), eq("key"), any()))
                .thenReturn(new CameraRtspTargetService.Inspection(null, DIGEST, null));
        assertThat(guard.invalidReason(check, camera, profile, null)).isNull();
        plan.setEndAt(local(NOW));
        assertThat(guard.invalidReason(check, camera, profile, null))
                .isEqualTo("PLAN_NO_LONGER_ACTIVE");
        plan.setEndAt(local(NOW.plusSeconds(30)));
        plan.setVersion(4L);
        when(crypto.digest("key", "camera-status-plan", "9/4")).thenReturn(new byte[] {4});
        assertThat(guard.invalidReason(check, camera, profile, null))
                .isEqualTo("PLAN_NO_LONGER_ACTIVE");
        plan.setVersion(3L);
        when(current.hasModuleAccess(1L, "camera")).thenReturn(false);
        assertThat(guard.invalidReason(check, camera, profile, null))
                .isEqualTo("PLAN_NO_LONGER_ACTIVE");
        verifyNoInteractions(repositories);
        verify(targets, times(1)).inspect(eq(camera), eq(profile), eq("key"), any());
    }

    private void allowUser() {
        var user = new UserEntity();
        user.setId(1L);
        user.setStatus(1);
        user.setMustChangePassword(false);
        when(users.getById(1L)).thenReturn(user);
        when(current.hasModuleAccess(1L, "camera")).thenReturn(true);
    }

    private static CameraStatusCheckEntity check() {
        var check = new CameraStatusCheckEntity();
        check.setActorUserId(1L);
        check.setCameraId(2L);
        check.setProfileId(3L);
        check.setSessionKeyId("key");
        check.setSessionDigest(DIGEST);
        check.setConnectionKeyId("key");
        check.setConnectionDigest(DIGEST);
        check.setDeadlineAt(local(NOW.plusSeconds(30)));
        return check;
    }

    private static CameraChannelEntity camera() {
        var camera = new CameraChannelEntity();
        camera.setId(2L);
        return camera;
    }

    private static CameraProfileEntity profile() {
        var profile = new CameraProfileEntity();
        profile.setId(3L);
        profile.setChannelId(2L);
        return profile;
    }

    private static LocalDateTime local(Instant instant) {
        return LocalDateTime.ofInstant(instant, ZONE);
    }
}
