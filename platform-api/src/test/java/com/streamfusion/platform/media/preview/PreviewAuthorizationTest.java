package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.service.UserService;
import java.time.Instant;
import org.junit.jupiter.api.Test;

class PreviewAuthorizationTest {
    final CameraAccessService cameras = mock(CameraAccessService.class);
    final CurrentUserService current = mock(CurrentUserService.class);
    final UserService users = mock(UserService.class);
    final PreviewAuthorization authorization =
            new PreviewAuthorization(cameras, current, mock(AccessService.class), users);
    final SessionPrincipalDto owner = new SessionPrincipalDto(1L, 0L, Instant.now());

    @Test
    void checksOwnershipBeforeCameraPermissionsCanFail() {
        when(current.principal()).thenReturn(new SessionPrincipalDto(2L, 0L, Instant.now()));
        assertThatThrownBy(() -> authorization.request(owner, 10L)).hasMessage("LEASE_NOT_FOUND");
        verifyNoInteractions(cameras, users);
    }

    @Test
    void anonymousCallerCannotTriggerAnOwnersCleanup() {
        when(current.principal()).thenThrow(BusinessException.error(ErrorCode.UNAUTHORIZED));
        assertThatThrownBy(() -> authorization.request(owner, 10L)).hasMessage("LEASE_NOT_FOUND");
        verifyNoInteractions(cameras, users);
    }

    @Test
    void sameOwnersRevokedPermissionKeepsItsBusinessFailure() {
        when(current.principal()).thenReturn(owner);
        var denied = BusinessException.error(ErrorCode.FORBIDDEN);
        when(cameras.readActor()).thenThrow(denied);
        assertThatThrownBy(() -> authorization.request(owner, 10L)).isSameAs(denied);
    }
}
