package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.mockito.Mockito.verifyNoMoreInteractions;
import static org.mockito.Mockito.when;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.controller.AuthController;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.security.ModuleAuthorizationInterceptor;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import java.util.Arrays;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.EnumSource;
import org.junit.jupiter.params.provider.ValueSource;
import org.slf4j.MDC;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import org.springframework.web.context.request.RequestContextHolder;
import org.springframework.web.context.request.ServletRequestAttributes;
import org.springframework.web.method.HandlerMethod;

/** 验证统一模块鉴权的默认拒绝、模块隔离以及认证边界。 */
class ModuleAuthorizationInterceptorTest {
    private static final long USER_ID = 9007199254740993L;
    private static final String TRACE = "b".repeat(32);
    private final ObjectMapper mapper = new ObjectMapper().findAndRegisterModules();
    private final CurrentUserService current = mock(CurrentUserService.class);
    private final ModuleAuthorizationService authorization =
            new ModuleAuthorizationService(current);
    private final ModuleAuthorizationInterceptor interceptor =
            new ModuleAuthorizationInterceptor(authorization, new ApiErrorWriter(mapper));

    @Test
    void classModuleGrantAllowsItsEndpoint() throws Exception {
        normalUser();
        when(current.hasModuleAccess(USER_ID, "user")).thenReturn(true);

        assertThat(invoke("/user/page", new UserEndpoints(), "list").getStatus()).isEqualTo(200);
        verify(current).requireNormal();
        verify(current).hasModuleAccess(USER_ID, "user");
        verifyNoMoreInteractions(current);
    }

    @Test
    void anotherModuleDoesNotGrantUserModule() throws Exception {
        normalUser();
        when(current.hasModuleAccess(USER_ID, "camera")).thenReturn(true);

        assertError(invoke("/user/page", new UserEndpoints(), "list"), ErrorCode.FORBIDDEN);
        verify(current).hasModuleAccess(USER_ID, "user");
    }

    @ParameterizedTest
    @ValueSource(
            strings = {
                "/user/undeclared",
                "/test-only",
                "/test-only/action",
                "/user/test-only",
                "/user/test-only/action",
                "/auth",
                "/auth/new-business"
            })
    void unannotatedBusinessControllerCannotBypassByPath(String path) throws Exception {
        assertError(invoke(path, new UndeclaredEndpoints(), "action"), ErrorCode.FORBIDDEN);
        verifyNoInteractions(current);
    }

    @Test
    void emptyModuleDoesNotFallBackToClassGrant() throws Exception {
        assertError(invoke("/user/empty", new UserEndpoints(), "empty"), ErrorCode.FORBIDDEN);
        verifyNoInteractions(current);
    }

    @Test
    void methodModuleOverridesClassModule() throws Exception {
        normalUser();
        when(current.hasModuleAccess(USER_ID, "camera")).thenReturn(true);

        assertThat(invoke("/user/camera", new UserEndpoints(), "camera").getStatus())
                .isEqualTo(200);
        verify(current).requireNormal();
        verify(current).hasModuleAccess(USER_ID, "camera");
        verifyNoMoreInteractions(current);
    }

    @Test
    void recheckRejectsGrantRevokedAfterRequestEntry() throws Exception {
        normalUser();
        when(current.hasModuleAccess(USER_ID, "user")).thenReturn(true, false);
        var request = new MockHttpServletRequest("POST", "/user");
        var response = new MockHttpServletResponse();
        var handler =
                new HandlerMethod(
                        new UserEndpoints(), UserEndpoints.class.getDeclaredMethod("list"));
        assertThat(interceptor.preHandle(request, response, handler)).isTrue();
        RequestContextHolder.setRequestAttributes(new ServletRequestAttributes(request));
        try {
            assertThatThrownBy(() -> authorization.recheckCurrentRequest(USER_ID))
                    .isInstanceOfSatisfying(
                            BusinessException.class,
                            error -> assertThat(error.code()).isEqualTo(ErrorCode.FORBIDDEN));
        } finally {
            RequestContextHolder.resetRequestAttributes();
        }
    }

    @ParameterizedTest
    @EnumSource(
            value = ErrorCode.class,
            names = {"UNAUTHORIZED", "PASSWORD_CHANGE_REQUIRED"})
    void sessionErrorsKeepTheirOriginalCode(ErrorCode code) throws Exception {
        when(current.requireNormal()).thenThrow(BusinessException.error(code));

        assertError(invoke("/user/page", new UserEndpoints(), "list"), code);
        verify(current).requireNormal();
        verifyNoMoreInteractions(current);
    }

    @ParameterizedTest
    @CsvSource({
        "csrf,GET,/auth/csrf",
        "csrf,HEAD,/auth/csrf",
        "login,POST,/auth/login",
        "me,GET,/auth/me",
        "update,PUT,/auth/me",
        "password,PUT,/auth/password",
        "logout,POST,/auth/logout"
    })
    void existingAuthEndpointUsesAuthenticationBoundary(String handler, String method, String path)
            throws Exception {
        assertThat(
                        interceptor.preHandle(
                                new MockHttpServletRequest(method, path),
                                new MockHttpServletResponse(),
                                authHandler(handler)))
                .isTrue();
        verifyNoInteractions(current);
    }

    @ParameterizedTest
    @CsvSource({"csrf,GET,/auth/new-business", "csrf,POST,/auth/csrf", "login,POST,/auth/logout"})
    void authenticationExemptionRequiresExactHandlerPathAndMethod(
            String handler, String method, String path) throws Exception {
        var request = new MockHttpServletRequest(method, path);
        var response = new MockHttpServletResponse();
        try (var ignored = MDC.putCloseable("traceId", TRACE)) {
            assertThat(interceptor.preHandle(request, response, authHandler(handler))).isFalse();
        }
        assertError(response, ErrorCode.FORBIDDEN);
        verifyNoInteractions(current);
    }

    private HandlerMethod authHandler(String method) {
        var reflected =
                Arrays.stream(AuthController.class.getDeclaredMethods())
                        .filter(candidate -> candidate.getName().equals(method))
                        .findFirst()
                        .orElseThrow();
        return new HandlerMethod(mock(AuthController.class), reflected);
    }

    private void normalUser() {
        var user = new UserEntity();
        user.setId(USER_ID);
        when(current.requireNormal()).thenReturn(user);
    }

    private MockHttpServletResponse invoke(String path, Object controller, String method)
            throws Exception {
        var request = new MockHttpServletRequest("GET", path);
        var response = new MockHttpServletResponse();
        var handler =
                new HandlerMethod(controller, controller.getClass().getDeclaredMethod(method));
        try (var ignored = MDC.putCloseable("traceId", TRACE)) {
            boolean allowed = interceptor.preHandle(request, response, handler);
            assertThat(allowed).isEqualTo(response.getStatus() == 200);
        }
        return response;
    }

    private void assertError(MockHttpServletResponse response, ErrorCode code) throws Exception {
        assertThat(response.getStatus()).isEqualTo(code.httpStatus());
        var body = mapper.readTree(response.getContentAsByteArray());
        assertThat(body.path("code").asText()).isEqualTo(code.name());
        assertThat(body.path("traceId").asText()).isEqualTo(TRACE);
    }

    @ModuleAccess("user")
    static class UserEndpoints {
        void list() {}

        @ModuleAccess(" ")
        void empty() {}

        @ModuleAccess("camera")
        void camera() {}
    }

    static class UndeclaredEndpoints {
        void action() {}
    }
}
