package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.service.CameraJobActor;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.service.UserService;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.context.SecurityContextImpl;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.session.*;

class CameraJobActorTest {
    private CameraJobActor actor;
    private UserEntity user;
    private MapSession session;
    private MapSessionRepository repository;
    private AccessMapper access;

    @BeforeEach
    @SuppressWarnings("unchecked")
    void setup() {
        repository = new MapSessionRepository(new java.util.concurrent.ConcurrentHashMap<>());
        ObjectProvider<SessionRepository<? extends Session>> provider = mock(ObjectProvider.class);
        doReturn(repository).when(provider).getIfAvailable();
        var users = mock(UserService.class);
        access = mock(AccessMapper.class);
        var properties = mock(AuthProperties.class);
        when(properties.absoluteTimeout()).thenReturn(Duration.ofHours(12));
        var current =
                new CurrentUserService(
                        users, mock(AccessService.class), properties, Clock.systemUTC());
        actor = new CameraJobActor(provider, current, users, access);
        user = new UserEntity();
        user.setId(91L);
        user.setStatus(UserStatus.NORMAL.getCode());
        user.setSessionVersion(3L);
        user.setMustChangePassword(false);
        when(users.getById(91L)).thenReturn(user);
        when(access.hasModuleAccess(91L, "camera")).thenReturn(true);
        when(access.isSuperAdmin(91L)).thenReturn(true);
        session = repository.createSession();
        session.setAttribute(
                HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY,
                new SecurityContextImpl(
                        UsernamePasswordAuthenticationToken.authenticated(
                                new SessionPrincipalDto(91L, 3L, Instant.now()), null, List.of())));
        repository.save(session);
    }

    @Test
    void validActorIsReadWithoutRenewingIdleTimeout() {
        Instant accessed = repository.findById(session.getId()).getLastAccessedTime();
        actor.check(91L, session.getId());
        assertThat(repository.findById(session.getId()).getLastAccessedTime()).isEqualTo(accessed);
    }

    @Test
    void logoutAndSessionVersionRevocationStopBackgroundWork() {
        user.setSessionVersion(4L);
        assertThatThrownBy(() -> actor.check(91L, session.getId()))
                .isInstanceOf(CameraAdapterException.class)
                .hasMessage("ACTOR_REVOKED");
        user.setSessionVersion(3L);
        repository.deleteById(session.getId());
        assertThatThrownBy(() -> actor.check(91L, session.getId()))
                .isInstanceOf(CameraAdapterException.class);
    }

    @Test
    void currentUserCannotStandInForSavedActor() {
        assertThatThrownBy(() -> actor.check(92L, session.getId()))
                .isInstanceOf(CameraAdapterException.class);
        user.setStatus(UserStatus.BANNED.getCode());
        assertThatThrownBy(() -> actor.check(91L, session.getId()))
                .isInstanceOf(CameraAdapterException.class);
    }

    @Test
    void moduleOrSuperAdminRevocationIsCheckedAgain() {
        when(access.hasModuleAccess(91L, "camera")).thenReturn(false);
        assertThatThrownBy(() -> actor.check(91L, session.getId()))
                .isInstanceOf(CameraAdapterException.class);
        when(access.hasModuleAccess(91L, "camera")).thenReturn(true);
        when(access.isSuperAdmin(91L)).thenReturn(false);
        assertThatThrownBy(() -> actor.check(91L, session.getId()))
                .isInstanceOf(CameraAdapterException.class);
    }
}
