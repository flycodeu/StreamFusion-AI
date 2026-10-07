package com.streamfusion.platform.camera.access.service;

import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.user.service.UserService;
import lombok.RequiredArgsConstructor;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.security.core.context.SecurityContext;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.session.Session;
import org.springframework.session.SessionRepository;
import org.springframework.stereotype.Service;

/** Checks the persisted session and current DB authority without fabricating a servlet request. */
@Service
@RequiredArgsConstructor
public class CameraJobActor {
    private final ObjectProvider<SessionRepository<? extends Session>> repositories;
    private final CurrentUserService current;
    private final UserService users;
    private final AccessMapper access;

    public void check(long userId, String sessionId) {
        var repository = repositories.getIfAvailable();
        if (repository == null) throw new CameraAdapterException("SESSION_STORE_UNAVAILABLE");
        var session = repository.findById(sessionId);
        if (session == null || session.isExpired()) throw revoked();
        Object stored =
                session.getAttribute(
                        HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY);
        if (!(stored instanceof SecurityContext context)
                || context.getAuthentication() == null
                || !context.getAuthentication().isAuthenticated()
                || !(context.getAuthentication().getPrincipal()
                        instanceof SessionPrincipalDto principal)
                || principal.getUserId() != userId) throw revoked();
        var user = users.getById(userId);
        try {
            current.validate(principal, user);
        } catch (com.streamfusion.platform.common.exception.BusinessException ex) {
            throw revoked();
        }
        if (CurrentUserService.requiresPasswordChange(user)
                || !access.isSuperAdmin(userId)
                || !access.hasModuleAccess(userId, "camera")) throw revoked();
    }

    private static CameraAdapterException revoked() {
        return new CameraAdapterException("ACTOR_REVOKED");
    }
}
