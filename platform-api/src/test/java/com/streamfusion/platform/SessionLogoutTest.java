package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doCallRealMethod;
import static org.mockito.Mockito.doThrow;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.spy;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.auth.controller.AuthController;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.security.SessionDependencyFilter;
import com.streamfusion.platform.auth.service.AuthenticationService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.auth.service.ProfileService;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.http.Cookie;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Base64;
import java.util.List;
import java.util.concurrent.ConcurrentHashMap;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.slf4j.MDC;
import org.springframework.dao.DataAccessResourceFailureException;
import org.springframework.data.redis.RedisConnectionFailureException;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.web.authentication.session.SessionAuthenticationStrategy;
import org.springframework.security.web.context.SecurityContextRepository;
import org.springframework.session.MapSession;
import org.springframework.session.MapSessionRepository;
import org.springframework.session.web.http.CookieHttpSessionIdResolver;
import org.springframework.session.web.http.DefaultCookieSerializer;
import org.springframework.session.web.http.SessionRepositoryFilter;

/** Exercises logout against the actual Spring Session wrapper with repository fault injection. */
class SessionLogoutTest {
    private final ObjectMapper json = new ObjectMapper().findAndRegisterModules();
    private final AuthenticationService authentication = mock(AuthenticationService.class);
    private final CurrentUserService current = mock(CurrentUserService.class);
    private final AuthController controller =
            new AuthController(
                    authentication,
                    mock(ProfileService.class),
                    current,
                    mock(SecurityContextRepository.class),
                    mock(SessionAuthenticationStrategy.class),
                    mock(AuthProperties.class));
    private final MapSessionRepository repository =
            spy(new MapSessionRepository(new ConcurrentHashMap<>()));
    private final SessionRepositoryFilter<MapSession> sessions =
            new SessionRepositoryFilter<>(repository);
    private final SessionDependencyFilter boundary =
            new SessionDependencyFilter(new ApiErrorWriter(json));
    private final SessionPrincipalDto principal = new SessionPrincipalDto(91, 0, Instant.now());
    private MapSession session;
    private Cookie cookie;

    @BeforeEach
    void prepare() {
        var serializer = new DefaultCookieSerializer();
        serializer.setCookieName("SF_SESSION");
        serializer.setCookiePath("/");
        var resolver = new CookieHttpSessionIdResolver();
        resolver.setCookieSerializer(serializer);
        sessions.setHttpSessionIdResolver(resolver);
        session = repository.createSession();
        session.setAttribute("synthetic", "identity");
        repository.save(session);
        cookie =
                new Cookie(
                        "SF_SESSION",
                        Base64.getEncoder()
                                .encodeToString(session.getId().getBytes(StandardCharsets.UTF_8)));
        when(current.principal()).thenReturn(principal);
        SecurityContextHolder.getContext()
                .setAuthentication(
                        UsernamePasswordAuthenticationToken.authenticated(
                                principal, null, List.of()));
    }

    @AfterEach
    void clearContext() {
        SecurityContextHolder.clearContext();
    }

    @Test
    void failedRevocationPreservesSessionCookieAndAllowsRetryWithoutSuccessAudit()
            throws Exception {
        doThrow(new RedisConnectionFailureException("synthetic deletion failure"))
                .doCallRealMethod()
                .when(repository)
                .deleteById(session.getId());

        var failure = logout();
        assertThat(failure.getStatus()).isEqualTo(503);
        assertThat(json.readTree(failure.getContentAsByteArray()).path("code").asText())
                .isEqualTo("DEPENDENCY_UNAVAILABLE");
        assertThat(failure.getHeaders("Set-Cookie"))
                .noneMatch(value -> value.contains("Max-Age=0"));
        assertThat(repository.findById(session.getId())).isNotNull();
        assertThat(SecurityContextHolder.getContext().getAuthentication()).isNotNull();
        verify(authentication, never()).recordLogout(any(), any());
        assertThat(failure.getContentAsString()).doesNotContain(session.getId(), cookie.getValue());

        var success = logout();
        assertThat(success.getStatus()).isEqualTo(200);
        assertThat(repository.findById(session.getId())).isNull();
        assertThat(SecurityContextHolder.getContext().getAuthentication()).isNull();
        assertThat(success.getHeaders("Set-Cookie")).anyMatch(value -> value.contains("Max-Age=0"));
        verify(authentication).recordLogout(any(), any());
    }

    @Test
    void confirmedRevocationSurvivesFailureOfWrappersSecondIdempotentDelete() throws Exception {
        doCallRealMethod()
                .doThrow(new RedisConnectionFailureException("synthetic second deletion failure"))
                .when(repository)
                .deleteById(session.getId());

        var response = logout();
        assertThat(response.getStatus()).isEqualTo(200);
        assertThat(repository.findById(session.getId())).isNull();
        assertThat(SecurityContextHolder.getContext().getAuthentication()).isNull();
        assertThat(response.getHeaders("Set-Cookie"))
                .anyMatch(value -> value.contains("Max-Age=0"));
        verify(authentication).recordLogout(any(), any());
    }

    @Test
    void confirmedRevocationRemainsSuccessfulWhenLogoutAuditIsUnavailable() throws Exception {
        doThrow(new DataAccessResourceFailureException("synthetic private audit payload"))
                .when(authentication)
                .recordLogout(any(), any());

        var response = logout();
        assertThat(response.getStatus()).isEqualTo(200);
        assertThat(json.readTree(response.getContentAsByteArray()).path("code").asText())
                .isEqualTo("SUCCESS");
        assertThat(repository.findById(session.getId())).isNull();
        assertThat(SecurityContextHolder.getContext().getAuthentication()).isNull();
        assertThat(response.getHeaders("Set-Cookie"))
                .anyMatch(value -> value.contains("Max-Age=0"));
        assertThat(response.getContentAsString())
                .doesNotContain(
                        "synthetic private audit payload", session.getId(), cookie.getValue());
        verify(authentication).recordLogout(any(), any());
    }

    @Test
    void logoutAuditProgrammingErrorsAreNotHiddenAfterRevocation() {
        var bug = new IllegalStateException("synthetic programming failure");
        doThrow(bug).when(authentication).recordLogout(any(), any());

        assertThatThrownBy(this::logout).isSameAs(bug);
        assertThat(repository.findById(session.getId())).isNull();
        assertThat(SecurityContextHolder.getContext().getAuthentication()).isNull();
    }

    private MockHttpServletResponse logout() throws Exception {
        var request = new MockHttpServletRequest("POST", "/auth/logout");
        request.setCookies(cookie);
        var response = new MockHttpServletResponse();
        try (var ignored = MDC.putCloseable("traceId", "a".repeat(32))) {
            boundary.doFilter(
                    request,
                    response,
                    (outerRequest, outerResponse) ->
                            sessions.doFilter(
                                    outerRequest,
                                    outerResponse,
                                    (sessionRequest, sessionResponse) -> {
                                        var httpRequest = (HttpServletRequest) sessionRequest;
                                        var httpResponse = (HttpServletResponse) sessionResponse;
                                        json.writeValue(
                                                httpResponse.getOutputStream(),
                                                controller.logout(httpRequest, httpResponse));
                                    }));
        }
        return response;
    }
}
