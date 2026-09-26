package com.streamfusion.platform.auth.service;

import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import lombok.RequiredArgsConstructor;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class CurrentUserService {
    private final UserService users;
    private final AccessService access;
    private final AuthProperties properties;
    private final Clock clock;

    public SessionPrincipalDto principal() {
        var authentication = SecurityContextHolder.getContext().getAuthentication();
        if (authentication == null
                || !authentication.isAuthenticated()
                || !(authentication.getPrincipal() instanceof SessionPrincipalDto principal)) {
            throw BusinessException.error(ErrorCode.UNAUTHORIZED);
        }
        return principal;
    }

    public UserEntity requireUser() {
        SessionPrincipalDto principal = principal();
        UserEntity user = users.getById(principal.getUserId());
        validate(principal, user);
        return user;
    }

    public void validate(SessionPrincipalDto principal, UserEntity user) {
        if (user == null
                || user.getStatus() == UserStatus.BANNED.getCode()
                || user.getSessionVersion() != principal.getSessionVersion()
                || !clock.instant()
                        .isBefore(
                                principal
                                        .getAuthenticatedAt()
                                        .plus(properties.absoluteTimeout()))) {
            throw BusinessException.error(ErrorCode.UNAUTHORIZED);
        }
    }

    public UserEntity requireNormal() {
        UserEntity user = requireUser();
        if (requiresPasswordChange(user))
            throw BusinessException.error(ErrorCode.PASSWORD_CHANGE_REQUIRED);
        return user;
    }

    /** Returns whether the current user has the controller's whole menu/function module. */
    public boolean hasModuleAccess(long userId, String module) {
        return access.hasModuleAccess(userId, module);
    }

    public static boolean requiresPasswordChange(UserEntity user) {
        return user.getStatus() == UserStatus.PENDING_PASSWORD.getCode()
                || Boolean.TRUE.equals(user.getMustChangePassword());
    }
}
