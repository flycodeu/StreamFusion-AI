package com.streamfusion.platform.diagnostics;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.service.UserService;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.servlet.mvc.method.RequestMappingInfo;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

class DiagnosticCaptureLimitsTest {
    @Test
    void expiryAndQuotaDoNotRequirePermissionQueriesForEveryRequest() throws Exception {
        var current = mock(CurrentUserService.class);
        var access = mock(AccessService.class);
        var users = mock(UserService.class);
        var admin = new UserEntity();
        admin.setId(1L);
        admin.setStatus(1);
        admin.setMustChangePassword(false);
        when(current.requireNormal()).thenReturn(admin);
        when(users.getById(1L)).thenReturn(admin);
        when(access.isSuperAdmin(1)).thenReturn(true);
        when(access.hasModuleAccess(1, "diagnostics")).thenReturn(true);
        var mappings = new RequestMappingHandlerMapping();
        var handler =
                new com.streamfusion.platform.camera.status.CameraStatusPlanController(null, null);
        var method = handler.getClass().getMethod("list");
        mappings.registerMapping(
                RequestMappingInfo.paths("/camera-status/plans")
                        .methods(org.springframework.web.bind.annotation.RequestMethod.GET)
                        .build(),
                handler,
                method);
        ObjectProvider<RequestMappingHandlerMapping> provider = mock(ObjectProvider.class);
        when(provider.getObject()).thenReturn(mappings);
        var clock = new MutableClock();
        var service =
                new DiagnosticCaptureService(
                        current, access, users, mock(AuditService.class), clock, provider);
        var command =
                new DiagnosticCaptureService.Command(
                        List.of("GET /camera-status/plans"), null, 1, false);
        service.start(command, null);
        assertThatThrownBy(() -> service.start(command, null))
                .isInstanceOf(com.streamfusion.platform.common.exception.BusinessException.class);
        SecurityContextHolder.getContext()
                .setAuthentication(
                        new UsernamePasswordAuthenticationToken(
                                new SessionPrincipalDto(1, 0, clock.instant()), null, List.of()));
        try {
            var request = new MockHttpServletRequest("GET", "/camera-status/plans");
            var hm = new org.springframework.web.method.HandlerMethod(handler, method);
            for (int i = 0; i < 1000; i++)
                assertThat(service.match(request, hm, "/camera-status/plans")).isNotNull();
            assertThat(service.match(request, hm, "/camera-status/plans")).isNull();
            assertThat(service.get().enabled()).isFalse();
            verify(users, atMost(2)).getById(1L);
            service.start(command, null);
            clock.at = clock.at.plusSeconds(61);
            assertThat(service.match(request, hm, "/camera-status/plans")).isNull();
            assertThat(service.get().enabled()).isFalse();
        } finally {
            SecurityContextHolder.clearContext();
        }
    }

    static class MutableClock extends Clock {
        Instant at = Instant.parse("2026-10-15T00:00:00Z");

        @Override
        public ZoneId getZone() {
            return ZoneOffset.UTC;
        }

        @Override
        public Clock withZone(ZoneId zone) {
            return this;
        }

        @Override
        public Instant instant() {
            return at;
        }
    }
}
