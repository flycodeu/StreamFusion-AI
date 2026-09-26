package com.streamfusion.platform.auth.security;

import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import com.streamfusion.platform.loginrecord.service.LoginRecordService;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.security.authentication.AnonymousAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.filter.OncePerRequestFilter;

@RequiredArgsConstructor
public class SessionGuardFilter extends OncePerRequestFilter {
    private static final Logger LOG = LoggerFactory.getLogger(SessionGuardFilter.class);
    private static final Set<String> LIMITED =
            Set.of(
                    "GET /auth/csrf",
                    "GET /auth/me",
                    "GET /auth/password-policy",
                    "PUT /auth/password",
                    "POST /auth/logout");
    private final CurrentUserService current;
    private final ApiErrorWriter writer;
    private final LoginRecordService loginRecords;

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        String route = request.getRequestURI().substring(request.getContextPath().length());
        String method = "HEAD".equals(request.getMethod()) ? "GET" : request.getMethod();
        boolean preparingCsrf = "GET".equals(method) && "/auth/csrf".equals(route);
        var authentication = SecurityContextHolder.getContext().getAuthentication();
        if (authentication != null
                && authentication.isAuthenticated()
                && !(authentication instanceof AnonymousAuthenticationToken)) {
            try {
                var user = current.requireUser();
                loginRecords.observe(request);
                if (CurrentUserService.requiresPasswordChange(user)
                        && !LIMITED.contains(method + " " + route)) {
                    throw BusinessException.error(ErrorCode.PASSWORD_CHANGE_REQUIRED);
                }
            } catch (BusinessException ex) {
                BusinessException reported = ex;
                if (ex.code() == ErrorCode.UNAUTHORIZED) {
                    var activity = loginRecords.activity(request);
                    if (!preparingCsrf
                            && authentication.getPrincipal()
                                    instanceof SessionPrincipalDto principal) {
                        reported = loginRecords.endedSession(principal, activity);
                    }
                    // Preparing a fresh login is an explicit anonymous-session transition.
                    // Its successful response must be allowed to carry the new CSRF cookie.
                    if (!preparingCsrf) SessionCookieProtectionFilter.revoked(request);
                    SecurityContextHolder.clearContext();
                    var session = request.getSession(false);
                    if (session != null) session.invalidate();
                    try {
                        loginRecords.end(activity, "SESSION_INVALIDATED", false);
                    } catch (RuntimeException failure) {
                        if (!SessionDependencyFilter.dependencyFailure(failure)) throw failure;
                        LOG.warn("Invalidated session history update unavailable");
                    }
                    if (preparingCsrf) {
                        chain.doFilter(request, response);
                        return;
                    }
                }
                writer.write(request, response, reported.code(), reported.safeDetails());
                return;
            }
        }
        chain.doFilter(request, response);
    }
}
