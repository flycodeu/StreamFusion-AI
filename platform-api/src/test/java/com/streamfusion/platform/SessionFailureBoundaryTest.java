package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.doNothing;
import static org.mockito.Mockito.doThrow;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.times;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.when;

import com.fasterxml.jackson.databind.DeserializationFeature;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.security.SessionDependencyFilter;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;
import org.slf4j.MDC;
import org.springframework.data.redis.RedisConnectionFailureException;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import org.springframework.session.MapSession;
import org.springframework.session.SessionRepository;
import org.springframework.session.web.http.SessionRepositoryFilter;

/** Exercises response completion through Spring Session without a Redis process. */
class SessionFailureBoundaryTest {
    private static final String TRACE = "d".repeat(32);
    private final ObjectMapper mapper = new ObjectMapper().findAndRegisterModules();
    private final SessionDependencyFilter boundary =
            new SessionDependencyFilter(new ApiErrorWriter(mapper));

    @Test
    void finalSessionSaveFailureReplacesBufferedSuccessWithOneCompleteError() throws Exception {
        MapSession session = new MapSession();
        SessionRepository<MapSession> repository = repository(session);
        doThrow(new RedisConnectionFailureException("synthetic Redis failure"))
                .when(repository)
                .save(session);
        var sessions = new SessionRepositoryFilter<>(repository);
        var request = new MockHttpServletRequest("POST", "/auth/login/secure");
        var response = new MockHttpServletResponse();

        try (var ignored = MDC.putCloseable("traceId", TRACE)) {
            byte[] success = mapper.writeValueAsBytes(R.success());
            boundary.doFilter(
                    request,
                    response,
                    (outerRequest, outerResponse) ->
                            sessions.doFilter(
                                    outerRequest,
                                    outerResponse,
                                    (sessionRequest, sessionResponse) -> {
                                        ((HttpServletRequest) sessionRequest)
                                                .getSession()
                                                .setAttribute("testIdentity", "synthetic");
                                        var httpResponse = (HttpServletResponse) sessionResponse;
                                        // Keep the body buffered so the failure happens in the
                                        // repository filter's final save, rather than a flush.
                                        httpResponse.setContentLength(success.length + 100);
                                        httpResponse.getOutputStream().write(success);
                                        assertThat(response.isCommitted()).isFalse();
                                        assertThat(response.getContentAsByteArray())
                                                .isEqualTo(success);
                                    }));
        }

        verify(repository).save(session);
        assertThat(response.getStatus()).isEqualTo(503);
        assertThat(response.getHeader("Content-Length")).isNull();
        assertThat(response.getHeader("Cache-Control")).isEqualTo("no-store");
        assertThat(response.getContentType()).startsWith("application/json");
        assertThat(response.getContentAsString()).doesNotContain("SUCCESS", "synthetic Redis");
        JsonNode error =
                mapper.reader()
                        .with(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
                        .readTree(response.getContentAsByteArray());
        assertThat(error.size()).isEqualTo(5);
        assertThat(error.path("code").asText()).isEqualTo("DEPENDENCY_UNAVAILABLE");
        assertThat(error.path("traceId").asText()).isEqualTo(TRACE);
        assertThat(error.path("msg").asText()).isNotBlank();
        assertThat(error.path("data").isNull()).isTrue();
        assertThat(error.hasNonNull("timestamp")).isTrue();
    }

    @Test
    void failureAfterResponseCommitDoesNotAppendOrOverwriteAnotherResponse() throws Exception {
        MapSession session = new MapSession();
        SessionRepository<MapSession> repository = repository(session);
        doNothing()
                .doThrow(new RedisConnectionFailureException("synthetic final save failure"))
                .when(repository)
                .save(session);
        var sessions = new SessionRepositoryFilter<>(repository);
        var request = new MockHttpServletRequest("GET", "/auth/me");
        var response = new MockHttpServletResponse();
        byte[] committed = "already committed".getBytes(StandardCharsets.UTF_8);

        try (var ignored = MDC.putCloseable("traceId", TRACE)) {
            boundary.doFilter(
                    request,
                    response,
                    (outerRequest, outerResponse) ->
                            sessions.doFilter(
                                    outerRequest,
                                    outerResponse,
                                    (sessionRequest, sessionResponse) -> {
                                        ((HttpServletRequest) sessionRequest)
                                                .getSession()
                                                .setAttribute("testIdentity", "synthetic");
                                        var httpResponse = (HttpServletResponse) sessionResponse;
                                        httpResponse.getOutputStream().write(committed);
                                        // The first save succeeds before this flush commits. The
                                        // filter's final save then fails after commitment.
                                        httpResponse.flushBuffer();
                                        assertThat(response.isCommitted()).isTrue();
                                    }));
        }

        verify(repository, times(2)).save(session);
        assertThat(response.getStatus()).isEqualTo(200);
        assertThat(response.getContentAsByteArray()).isEqualTo(committed);
        assertThat(response.getHeader("Cache-Control")).isNull();
    }

    @Test
    void nonDependencyFailureEscapesWithoutBeingRelabeledAsAnOutage() {
        MapSession session = new MapSession();
        SessionRepository<MapSession> repository = repository(session);
        IllegalStateException failure = new IllegalStateException("synthetic programming failure");
        doThrow(failure).when(repository).save(session);
        var sessions = new SessionRepositoryFilter<>(repository);
        var request = new MockHttpServletRequest("GET", "/auth/me");
        var response = new MockHttpServletResponse();

        try (var ignored = MDC.putCloseable("traceId", TRACE)) {
            assertThatThrownBy(
                            () ->
                                    boundary.doFilter(
                                            request,
                                            response,
                                            (outerRequest, outerResponse) ->
                                                    sessions.doFilter(
                                                            outerRequest,
                                                            outerResponse,
                                                            (sessionRequest, sessionResponse) ->
                                                                    ((HttpServletRequest)
                                                                                    sessionRequest)
                                                                            .getSession()
                                                                            .setAttribute(
                                                                                    "testIdentity",
                                                                                    "synthetic"))))
                    .isSameAs(failure);
        }

        verify(repository).save(session);
        assertThat(response.getContentAsByteArray()).isEmpty();
        assertThat(response.getStatus()).isEqualTo(200);
    }

    @SuppressWarnings("unchecked")
    private static SessionRepository<MapSession> repository(MapSession session) {
        SessionRepository<MapSession> repository = mock(SessionRepository.class);
        when(repository.createSession()).thenReturn(session);
        return repository;
    }
}
