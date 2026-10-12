package com.streamfusion.platform.preview;

import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.user.service.UserService;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Request and background checks share the current account, PAGE and camera scope rules. */
@Component
@RequiredArgsConstructor
public class PreviewAuthorization {
    private final CameraAccessService cameras;
    private final CurrentUserService current;
    private final AccessService access;
    private final UserService users;

    public SessionPrincipalDto principal(long cameraId) {
        var actor = cameras.readActor();
        cameras.requireVisible(cameraId, actor);
        return current.principal();
    }

    public void request(SessionPrincipalDto owner, long cameraId) {
        var actor = cameras.readActor();
        if (actor.userId() != owner.getUserId()) throw new PreviewFailure("LEASE_NOT_FOUND");
        current.validate(owner, users.getById(owner.getUserId()));
        cameras.requireVisible(cameraId, actor);
    }

    public boolean stillAllowed(SessionPrincipalDto owner, long cameraId) {
        try {
            var user = users.getById(owner.getUserId());
            current.validate(owner, user);
            if (CurrentUserService.requiresPasswordChange(user)
                    || !current.hasModuleAccess(owner.getUserId(), "camera")) return false;
            cameras.requireVisible(
                    cameraId,
                    new CameraAccessService.Actor(
                            owner.getUserId(), access.isSuperAdmin(owner.getUserId())));
            return true;
        } catch (com.streamfusion.platform.common.exception.BusinessException ex) {
            return false;
        }
    }
}
