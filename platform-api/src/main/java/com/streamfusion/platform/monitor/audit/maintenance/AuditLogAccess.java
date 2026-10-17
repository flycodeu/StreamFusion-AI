package com.streamfusion.platform.monitor.audit.maintenance;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.system.access.mapper.AccessMapper;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.service.UserService;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

@Component
@RequiredArgsConstructor
public class AuditLogAccess {
    private final CurrentUserService current;
    private final AccessService access;
    private final UserService users;
    private final AccessMapper coordination;

    /** The caller owns the write transaction; authorization follows the shared identity lock. */
    public long lockActor() {
        lockAuthorization();
        return actor();
    }

    public long lockAdministrator() {
        lockAuthorization();
        return administrator();
    }

    private void lockAuthorization() {
        if (coordination.lockSuperAdminRole() == null)
            throw BusinessException.error(ErrorCode.FORBIDDEN);
    }

    public long actor() {
        long id = current.requireNormal().getId();
        if (!access.hasModuleAccess(id, "audit"))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        return id;
    }

    public long administrator() {
        long id = actor();
        if (!access.isSuperAdmin(id)) throw BusinessException.error(ErrorCode.FORBIDDEN);
        return id;
    }

    public boolean superAdmin(long id) {
        return access.isSuperAdmin(id);
    }

    public boolean exportAllowed(long id) {
        var user = users.getById(id);
        return user != null
                && user.getStatus() == 1
                && !Boolean.TRUE.equals(user.getMustChangePassword())
                && access.hasModuleAccess(id, "audit");
    }
}
