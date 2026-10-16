package com.streamfusion.platform.media.preview;

import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.service.UserService;
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

    public void capacity() {
        cameras.readActor();
    }

    public SessionPrincipalDto principal(long cameraId) {
        var actor = cameras.readActor();
        cameras.requireVisible(cameraId, actor);
        return current.principal();
    }

    public void request(SessionPrincipalDto owner, long cameraId) {
        SessionPrincipalDto caller;
        try {
            caller = current.principal();
        } catch (BusinessException unauthenticated) {
            throw new PreviewFailure("LEASE_NOT_FOUND");
        }
        if (caller.getUserId() != owner.getUserId()) throw new PreviewFailure("LEASE_NOT_FOUND");
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
