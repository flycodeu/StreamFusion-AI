package com.streamfusion.platform.auth.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.auth.pojo.dto.LoginDto;
import com.streamfusion.platform.auth.pojo.dto.PasswordChangeDto;
import com.streamfusion.platform.auth.pojo.vo.AuthUserVo;
import com.streamfusion.platform.auth.pojo.vo.CsrfVo;
import com.streamfusion.platform.auth.security.SessionDependencyFilter;
import com.streamfusion.platform.auth.service.AuthenticationService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.auth.service.ProfileService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.user.pojo.dto.UserProfileUpdateDto;
import com.streamfusion.platform.user.pojo.vo.UserProfileVo;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.http.HttpHeaders;
import org.springframework.http.ResponseCookie;
import org.springframework.security.authentication.AnonymousAuthenticationToken;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.web.authentication.session.SessionAuthenticationStrategy;
import org.springframework.security.web.context.SecurityContextRepository;
import org.springframework.security.web.csrf.CsrfToken;
import org.springframework.session.SessionRepository;
import org.springframework.session.web.http.SessionRepositoryFilter;
import org.springframework.web.bind.annotation.*;

@Tag(name = "认证与个人资料")
@RestController
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
@RequestMapping("/auth")
@RequiredArgsConstructor
public class AuthController {
    private static final Logger LOG = LoggerFactory.getLogger(AuthController.class);
    private final AuthenticationService authentication;
    private final ProfileService profiles;
    private final CurrentUserService current;
    private final SecurityContextRepository contexts;
    private final SessionAuthenticationStrategy strategy;
    private final AuthProperties properties;

    @Operation(summary = "获取 CSRF 信息")
    @GetMapping("/csrf")
    public R<CsrfVo> csrf(CsrfToken token) {
        return R.success(new CsrfVo(token.getHeaderName(), token.getToken()));
    }

    @Operation(summary = "登录")
    @PostMapping("/login")
    public R<Void> login(
            @RequestBody LoginDto input, HttpServletRequest request, HttpServletResponse response) {
        var existing = SecurityContextHolder.getContext().getAuthentication();
        if (existing != null
                && existing.isAuthenticated()
                && !(existing instanceof AnonymousAuthenticationToken)) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        var principal =
                authentication.authenticate(
                        input.getUsername(), input.getPassword(), AuditContextDto.from(request));
        var auth = UsernamePasswordAuthenticationToken.authenticated(principal, null, List.of());
        try {
            strategy.onAuthentication(auth, request, response);
            request.getSession()
                    .setMaxInactiveInterval(Math.toIntExact(properties.idleTimeout().toSeconds()));
            var context = SecurityContextHolder.createEmptyContext();
            context.setAuthentication(auth);
            SecurityContextHolder.setContext(context);
            // Redis flush-mode=immediate persists this attribute before the successful response.
            contexts.saveContext(context, request, response);
            authentication.recordLogin(principal, AuditContextDto.from(request));
        } catch (RuntimeException ex) {
            SecurityContextHolder.clearContext();
            try {
                if (request.getSession(false) != null) request.getSession(false).invalidate();
            } catch (RuntimeException cleanup) {
                LOG.warn("Failed login session cleanup could not complete");
            }
            expireCookie(response);
            throw ex;
        }
        return R.success();
    }

    @Operation(summary = "获取当前用户")
    @GetMapping("/me")
    public R<AuthUserVo> me() {
        return R.success(profiles.me());
    }

    @Operation(summary = "修改个人资料")
    @PutMapping("/me")
    public R<UserProfileVo> update(
            @RequestBody UserProfileUpdateDto input, HttpServletRequest request) {
        return R.success(profiles.update(input, AuditContextDto.from(request)));
    }

    @Operation(summary = "修改密码")
    @PutMapping("/password")
    public R<Void> password(
            @RequestBody PasswordChangeDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        profiles.changePassword(
                input.getCurrentPassword(), input.getNewPassword(), AuditContextDto.from(request));
        try {
            destroySession(request, response);
        } catch (RuntimeException ex) {
            if (!SessionDependencyFilter.dependencyFailure(ex)) throw ex;
            // Committed session_version already rejects all old sessions, even if Redis cleanup
            // fails.
            SecurityContextHolder.clearContext();
            expireCookie(response);
            LOG.warn("Password changed; physical session cleanup unavailable");
        }
        return R.success();
    }

    @Operation(summary = "退出登录")
    @PostMapping("/logout")
    public R<Void> logout(HttpServletRequest request, HttpServletResponse response) {
        var principal = current.principal();
        destroySession(request, response);
        try {
            authentication.recordLogout(principal, AuditContextDto.from(request));
        } catch (RuntimeException ex) {
            if (!SessionDependencyFilter.dependencyFailure(ex)) throw ex;
            // Revocation already succeeded; an unavailable audit store must not misreport logout.
            LOG.error(
                    "Logout audit unavailable after session revocation userId={}",
                    principal.getUserId());
        }
        return R.success();
    }

    private void destroySession(HttpServletRequest request, HttpServletResponse response) {
        var session = request.getSession(false);
        if (session != null) {
            Object candidate =
                    request.getAttribute(SessionRepositoryFilter.SESSION_REPOSITORY_ATTR);
            if (candidate instanceof SessionRepository<?> repository) {
                // Spring Session invalidates its local wrapper before deleting from Redis.
                // Delete first so a failed attempt preserves the cookie and permits retry.
                repository.deleteById(session.getId());
                try {
                    session.invalidate();
                } catch (RuntimeException ex) {
                    if (!SessionDependencyFilter.dependencyFailure(ex)) throw ex;
                    // The first deletion already succeeded; the wrapper is now invalid and will
                    // not recreate the session when the request completes.
                    LOG.warn("Revoked session wrapper cleanup could not complete");
                }
            } else {
                // Servlet-only contexts (including MockMvc) have no external session repository.
                session.invalidate();
            }
        }
        SecurityContextHolder.clearContext();
        expireCookie(response);
    }

    private void expireCookie(HttpServletResponse response) {
        response.addHeader(
                HttpHeaders.SET_COOKIE,
                ResponseCookie.from("SF_SESSION", "")
                        .path("/")
                        .httpOnly(true)
                        .secure(properties.secureCookie())
                        .sameSite("Lax")
                        .maxAge(0)
                        .build()
                        .toString());
    }
}
