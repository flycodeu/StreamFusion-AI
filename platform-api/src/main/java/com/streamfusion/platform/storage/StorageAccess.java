package com.streamfusion.platform.storage;

import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.pojo.enums.UserStatus;
import com.streamfusion.platform.system.user.service.UserService;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

@Component
@RequiredArgsConstructor
public class StorageAccess {
    private final CurrentUserService current;
    private final ModuleAuthorizationService authorization;
    private final UserService users;

    public long actor() {
        return current.requireNormal().getId();
    }

    public void recheck(long actor) {
        authorization.recheckCurrentRequest(actor);
    }

    public void background(long actor) {
        var user = users.getById(actor);
        if (user == null
                || user.getStatus() == UserStatus.BANNED.getCode()
                || CurrentUserService.requiresPasswordChange(user)
                || !current.hasModuleAccess(actor, "storage"))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
    }
}
