package com.streamfusion.platform.camera.service;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.entity.*;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.web.context.request.RequestContextHolder;
import org.springframework.web.context.request.ServletRequestAttributes;

class CameraGroupChangePlannerTest {
    private final CameraScopeMapper scopes = mock(CameraScopeMapper.class);
    private final CameraAccessService access = mock(CameraAccessService.class);
    private final Instant now = Instant.parse("2026-10-17T02:00:00Z");
    private final Clock clock = Clock.fixed(now, ZoneOffset.UTC);
    private final CameraAccessService.Actor actor = new CameraAccessService.Actor(7L, true);
    private final CameraCryptoService crypto = crypto();
    private final CameraGroupChangePlanner changes =
            new CameraGroupChangePlanner(scopes, access, crypto, clock);
    private final CameraGroupTree tree =
            new CameraGroupTree(List.of(group(1, null, 8), group(2, 1L, 4), group(3, 1L, 5)));
    private final CameraUserScopeEntity scope = new CameraUserScopeEntity();
    private final CameraChannelScopeRow affected = new CameraChannelScopeRow();
    private final MockHttpServletRequest request = new MockHttpServletRequest();

    @BeforeEach
    void setup() {
        scope.setUserId(20L);
        scope.setVersion(6L);
        affected.setId(10L);
        affected.setVersion(9L);
        affected.setGroupId(2L);
        affected.setLifecycle("ENABLED");
        when(scopes.scopeVersions()).thenReturn(List.of(scope));
        when(scopes.authorizationRevisions()).thenReturn(List.of("authorization:20:0"));
        when(scopes.channelsInGroups(anyList())).thenReturn(List.of(affected));
        request.setSession(new MockHttpSession());
        RequestContextHolder.setRequestAttributes(new ServletRequestAttributes(request));
    }

    @AfterEach
    void clearRequest() {
        RequestContextHolder.resetRequestAttributes();
    }

    @Test
    void confirmationRetainsTheExistingPurposeAndOrderedHmacPayload() {
        long expires = now.plusSeconds(300).getEpochSecond();
        var payload =
                List.of(
                        "GROUP_MOVE",
                        actor.userId(),
                        request.getSession(false).getId(),
                        2L,
                        4L,
                        "3",
                        "",
                        expires,
                        List.of(List.of(1L, "", 8L), List.of(2L, "1", 4L), List.of(3L, "1", 5L)),
                        List.of(List.of(20L, 6L)),
                        List.of("authorization:20:0"),
                        List.of(List.of(10L, 9L, "2", "ENABLED")));
        String token = changes.confirmation("GROUP_MOVE", actor, 2, 4, 3L, "", tree);
        assertThat(token).isEqualTo(expires + "." + crypto.sign("camera-impact", payload));
        verifyToken(token, actor);
        verifyNoInteractions(access);
    }

    @Test
    void accountSessionTreeScopeRoleAndAffectedMembershipChangesInvalidatePreparedMove() {
        String token = changes.confirmation("GROUP_MOVE", actor, 2, 4, 3L, "", tree);
        rejects(token, new CameraAccessService.Actor(8L, true));
        var session = request.getSession(false);
        request.setSession(new MockHttpSession());
        rejects(token, actor);
        request.setSession((MockHttpSession) session);
        tree.require(3).setVersion(6L);
        rejects(token, actor);
        tree.require(3).setVersion(5L);
        scope.setVersion(7L);
        rejects(token, actor);
        scope.setVersion(6L);
        when(scopes.authorizationRevisions()).thenReturn(List.of("authorization:20:1"));
        rejects(token, actor);
        when(scopes.authorizationRevisions()).thenReturn(List.of("authorization:20:0"));
        affected.setVersion(10L);
        rejects(token, actor);
        affected.setVersion(9L);
        verifyToken(token, actor);
    }

    @Test
    void expiredConfirmationAndMissingSessionFailBeforeReusingProof() {
        String token = changes.confirmation("GROUP_MOVE", actor, 2, 4, 3L, "", tree);
        var expired =
                new CameraGroupChangePlanner(
                        scopes, access, crypto, Clock.offset(clock, Duration.ofSeconds(300)));
        assertThatThrownBy(() -> expired.verify("GROUP_MOVE", actor, 2, 4, 3L, "", token, tree))
                .isInstanceOf(BusinessException.class);
        RequestContextHolder.resetRequestAttributes();
        clearInvocations(scopes);
        assertThatThrownBy(() -> changes.confirmation("GROUP_MOVE", actor, 2, 4, 3L, "", tree))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> assertThat(error.code()).isEqualTo(ErrorCode.UNAUTHORIZED));
        verifyNoInteractions(scopes);
    }

    @Test
    void backgroundImportScopeProofNeedsNoHttpSessionButTracksIdentityAndAuthorization() {
        RequestContextHolder.resetRequestAttributes();
        String fingerprint = changes.importScopeFingerprint(actor, 3L, tree);
        assertThat(changes.matchesImportScope(actor, 3L, fingerprint, tree)).isTrue();
        assertThat(changes.matchesImportScope(actor, 2L, fingerprint, tree)).isFalse();
        scope.setVersion(7L);
        assertThat(changes.matchesImportScope(actor, 3L, fingerprint, tree)).isFalse();
        verify(scopes, never()).channelsInGroups(anyList());
    }

    @Test
    void deviceImpactRetainsDirectGrantsSkipsDisabledMembersAndExcludesSuperAdmins() {
        when(scopes.groupGrantsForImpact(anyList()))
                .thenReturn(List.of(grant(100, 1), grant(200, 2), grant(300, 3), grant(900, 3)));
        when(scopes.directGrantsForImpact(anyList())).thenReturn(List.of(grant(200, 10)));
        when(access.superAdminIds(anyCollection())).thenReturn(Set.of(900L));
        var impact =
                changes.deviceImpact(
                        List.of(member(10, "ENABLED"), member(11, "DISABLED")), tree, 3);
        assertThat(impact)
                .containsEntry("gainedUserCount", 1)
                .containsEntry("lostUserCount", 0)
                .containsEntry("computedAt", now);
        verify(scopes).directGrantsForImpact(List.of(10L, 11L));
    }

    private void verifyToken(String token, CameraAccessService.Actor current) {
        changes.verify("GROUP_MOVE", current, 2, 4, 3L, "", token, tree);
    }

    private void rejects(String token, CameraAccessService.Actor current) {
        assertThatThrownBy(() -> verifyToken(token, current))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> assertThat(error.code()).isEqualTo(ErrorCode.CONFLICT));
    }

    private static CameraGroupEntity group(long id, Long parent, long version) {
        var row = new CameraGroupEntity();
        row.setId(id);
        row.setParentId(parent);
        row.setName("Group " + id);
        row.setVersion(version);
        return row;
    }

    private static CameraChannelEntity member(long id, String state) {
        var row = new CameraChannelEntity();
        row.setId(id);
        row.setGroupId(2L);
        row.setLifecycle(state);
        return row;
    }

    private static CameraGrantRow grant(long user, long target) {
        var row = new CameraGrantRow();
        row.setUserId(user);
        row.setTargetId(target);
        return row;
    }

    private static CameraCryptoService crypto() {
        var properties = new CameraProperties();
        properties.setActiveKeyId("fixture");
        properties.getKeys().put("fixture", Base64.getEncoder().encodeToString(new byte[32]));
        return new CameraCryptoService(properties, new ObjectMapper());
    }
}
