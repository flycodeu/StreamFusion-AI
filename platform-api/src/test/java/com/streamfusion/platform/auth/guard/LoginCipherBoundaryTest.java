package com.streamfusion.platform.auth.guard;

import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.pojo.dto.EncryptedLoginDto;
import com.streamfusion.platform.auth.service.LoginCipherService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpSession;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import org.junit.jupiter.api.Test;
import org.springframework.data.redis.RedisConnectionFailureException;
import org.springframework.mock.web.MockHttpServletRequest;

class LoginCipherBoundaryTest {
    private static final Instant TIME = Instant.parse("2026-09-26T00:00:00Z");

    @Test
    void expiredChallengeCannotClaimOrDecryptAndRedisFailureCannotPermitAReplay() {
        var store = mock(LoginGuardStore.class);
        var request = new MockHttpServletRequest();
        var service = service(TIME, store);
        var challenge = service.challenge(request);
        var envelope = new EncryptedLoginDto(challenge.challengeId(), "unused", "unused", "unused");
        assertThatThrownBy(() -> service(TIME.plusSeconds(121), store).decrypt(envelope, request))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        ex ->
                                org.assertj.core.api.Assertions.assertThat(ex.code())
                                        .isEqualTo(ErrorCode.VALIDATION_ERROR));
        verifyNoInteractions(store);
        var fresh = service.challenge(request);
        when(store.claimChallenge(anyString()))
                .thenThrow(new RedisConnectionFailureException("synthetic failure"));
        assertThatThrownBy(
                        () ->
                                service.decrypt(
                                        new EncryptedLoginDto(
                                                fresh.challengeId(), "unused", "unused", "unused"),
                                        request))
                .isInstanceOf(RedisConnectionFailureException.class);
    }

    @Test
    void challengeSessionDependencyFailureIsSafe503() {
        var request = mock(HttpServletRequest.class);
        var session = mock(HttpSession.class);
        when(request.getSession(true)).thenReturn(session);
        doThrow(new RedisConnectionFailureException("private failure"))
                .when(session)
                .setAttribute(anyString(), any());
        assertThatThrownBy(() -> service(TIME, mock(LoginGuardStore.class)).challenge(request))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        ex ->
                                org.assertj.core.api.Assertions.assertThat(ex.code())
                                        .isEqualTo(ErrorCode.DEPENDENCY_UNAVAILABLE));
    }

    private LoginCipherService service(Instant now, LoginGuardStore store) {
        return new LoginCipherService(Clock.fixed(now, ZoneOffset.UTC), new ObjectMapper(), store);
    }
}
