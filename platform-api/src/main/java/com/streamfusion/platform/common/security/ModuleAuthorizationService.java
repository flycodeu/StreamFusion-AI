package com.streamfusion.platform.common.security;

import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import jakarta.servlet.http.HttpServletRequest;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.web.context.request.RequestAttributes;
import org.springframework.web.context.request.RequestContextHolder;
import org.springframework.web.context.request.ServletRequestAttributes;

/**
 * Stores the module resolved at the request boundary and rechecks it after a business lock.
 *
 * <p>The request boundary remains the only place that resolves a controller module. The second
 * check closes the wait-for-lock window without making services carry endpoint permission codes.
 */
@Service
@RequiredArgsConstructor
public class ModuleAuthorizationService {
    private static final String MODULE_ATTRIBUTE =
            ModuleAuthorizationService.class.getName() + ".module";
    private static final String USER_ATTRIBUTE =
            ModuleAuthorizationService.class.getName() + ".userId";

    private final CurrentUserService currentUser;

    /** Checks and records the module selected by the MVC interceptor. */
    public void authorize(HttpServletRequest request, String module) {
        var user = currentUser.requireNormal();
        if (!currentUser.hasModuleAccess(user.getId(), module)) {
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        }
        request.setAttribute(MODULE_ATTRIBUTE, module);
        request.setAttribute(USER_ATTRIBUTE, user.getId());
    }

    /**
     * Rechecks the same module for the actor after the workflow's coordination lock is acquired.
     * Authorization changes must use that same coordination boundary before changing role/menu
     * bindings so this check cannot silently accept a stale request snapshot.
     */
    public void recheckCurrentRequest(long userId) {
        HttpServletRequest request = currentRequest();
        Object recordedUser = request.getAttribute(USER_ATTRIBUTE);
        Object recordedModule = request.getAttribute(MODULE_ATTRIBUTE);
        if (!(recordedUser instanceof Long authorizedUserId)
                || authorizedUserId != userId
                || !(recordedModule instanceof String module)
                || module.isBlank()
                || !currentUser.hasModuleAccess(userId, module)) {
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        }
    }

    private static HttpServletRequest currentRequest() {
        RequestAttributes attributes = RequestContextHolder.getRequestAttributes();
        if (attributes instanceof ServletRequestAttributes servlet) {
            return servlet.getRequest();
        }
        throw BusinessException.error(ErrorCode.FORBIDDEN);
    }
}
