package com.streamfusion.platform.auth.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.auth.guard.LoginProtection;
import com.streamfusion.platform.auth.pojo.dto.EncryptedLoginDto;
import com.streamfusion.platform.auth.pojo.dto.LoginDto;
import com.streamfusion.platform.auth.pojo.dto.PasswordChangeDto;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.pojo.vo.AuthUserVo;
import com.streamfusion.platform.auth.pojo.vo.CsrfVo;
import com.streamfusion.platform.auth.pojo.vo.LoginChallengeVo;
import com.streamfusion.platform.auth.pojo.vo.PasswordPolicyVo;
import com.streamfusion.platform.auth.security.SessionCookieProtectionFilter;
import com.streamfusion.platform.auth.security.SessionDependencyFilter;
import com.streamfusion.platform.auth.service.AuthenticationService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.auth.service.LoginCipherService;
import com.streamfusion.platform.auth.service.ProfileService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.loginrecord.pojo.vo.LoginRecordVo;
import com.streamfusion.platform.loginrecord.service.LoginRecordService;
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
import org.springdoc.core.annotations.ParameterObject;
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
    public static final String LOGIN_ESTABLISHING =
            AuthController.class.getName() + ".establishing";
    private final AuthenticationService authentication;
    private final ProfileService profiles;
    private final CurrentUserService current;
    private final SecurityContextRepository contexts;
    private final SessionAuthenticationStrategy strategy;
    private final AuthProperties properties;
    private final LoginCipherService loginCipher;
    private final LoginProtection protection;
    private final LoginRecordService loginRecords;

    @Operation(summary = "获取 CSRF 信息")
    @GetMapping("/csrf")
    public R<CsrfVo> csrf(CsrfToken token) {
        return R.success(new CsrfVo(token.getHeaderName(), token.getToken()));
    }

    @Operation(summary = "获取一次性登录加密挑战")
    @GetMapping("/login/challenge")
    public R<LoginChallengeVo> loginChallenge(
            HttpServletRequest request, HttpServletResponse response) {
        response.setHeader(HttpHeaders.CACHE_CONTROL, "no-store");
        return R.success(loginCipher.challenge(request));
    }

    @Operation(summary = "加密登录")
    @PostMapping("/login/secure")
    public R<Void> secureLogin(
            @RequestBody EncryptedLoginDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return protection.attempt(
                request,
                () -> completeLogin(loginCipher.decrypt(input, request), request, response));
    }

    private R<Void> completeLogin(
            LoginDto input, HttpServletRequest request, HttpServletResponse response) {
        var existing = SecurityContextHolder.getContext().getAuthentication();
        if (existing != null
                && existing.isAuthenticated()
                && !(existing instanceof AnonymousAuthenticationToken)) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        try {
            authentication.authenticate(
                    input.getUsername(),
                    input.getPassword(),
                    AuditContextDto.from(request),
                    principal -> establishSession(principal, request, response));
        } catch (RuntimeException ex) {
            // Rejected credentials have not created authenticated state. Keep their anonymous
            // CSRF session so correcting the password works without an unrelated CSRF retry.
            if (!Boolean.TRUE.equals(request.getAttribute(LOGIN_ESTABLISHING))) throw ex;
            SessionCookieProtectionFilter.revoked(request);
            SecurityContextHolder.clearContext();
            try {
                loginRecords.end(loginRecords.activity(request), "LOGIN_ABORTED", false);
            } catch (RuntimeException cleanup) {
                LOG.warn("Failed login history cleanup could not complete");
            }
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

    private void establishSession(
            SessionPrincipalDto principal,
            HttpServletRequest request,
            HttpServletResponse response) {
        request.setAttribute(LOGIN_ESTABLISHING, Boolean.TRUE);
        var auth = UsernamePasswordAuthenticationToken.authenticated(principal, null, List.of());
        strategy.onAuthentication(auth, request, response);
        request.getSession()
                .setMaxInactiveInterval(Math.toIntExact(properties.idleTimeout().toSeconds()));
        loginRecords.start(
                principal, request, established -> persistSession(established, request, response));
    }

    private void persistSession(
            SessionPrincipalDto principal,
            HttpServletRequest request,
            HttpServletResponse response) {
        var context = SecurityContextHolder.createEmptyContext();
        context.setAuthentication(
                UsernamePasswordAuthenticationToken.authenticated(principal, null, List.of()));
        SecurityContextHolder.setContext(context);
        // Immediate Redis writes must succeed before the SQL transaction commits.
        contexts.saveContext(context, request, response);
        authentication.recordLogin(principal, AuditContextDto.from(request));
    }

    @Operation(summary = "获取当前用户")
    @GetMapping("/me")
    public R<AuthUserVo> me() {
        return R.success(profiles.me());
    }

    @Operation(summary = "分页查询本人的成功登录历史")
    @GetMapping("/login-records/page")
    public R<PageResultVo<LoginRecordVo>> loginRecords(
            @ParameterObject @ModelAttribute PageQueryDto query) {
        return R.success(loginRecords.mine(query));
    }

    @Operation(summary = "获取当前密码规则")
    @GetMapping("/password-policy")
    public R<PasswordPolicyVo> passwordPolicy() {
        current.requireUser();
        return R.success(
                new PasswordPolicyVo(
                        properties.minPasswordLength(),
                        properties.maxPasswordLength(),
                        properties.requireUppercase(),
                        properties.requireLowercase(),
                        properties.requireDigit(),
                        properties.requireSymbol()));
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
        var activity = loginRecords.activity(request);
        destroySession(request, response);
        try {
            loginRecords.end(activity, "LOGOUT", true);
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
