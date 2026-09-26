package com.streamfusion.platform.auth.security;

import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.security.authentication.AnonymousAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.filter.OncePerRequestFilter;

@RequiredArgsConstructor
public class SessionGuardFilter extends OncePerRequestFilter {
    private static final Set<String> LIMITED =
            Set.of(
                    "GET /auth/csrf", "GET /auth/me",
                    "PUT /auth/password", "POST /auth/logout");
    private final CurrentUserService current;
    private final ApiErrorWriter writer;

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        var authentication = SecurityContextHolder.getContext().getAuthentication();
        if (authentication != null
                && authentication.isAuthenticated()
                && !(authentication instanceof AnonymousAuthenticationToken)) {
            try {
                var user = current.requireUser();
                String route = request.getRequestURI().substring(request.getContextPath().length());
                if (CurrentUserService.requiresPasswordChange(user)
                        && !LIMITED.contains(request.getMethod() + " " + route)) {
                    throw BusinessException.error(ErrorCode.PASSWORD_CHANGE_REQUIRED);
                }
            } catch (BusinessException ex) {
                if (ex.code() == ErrorCode.UNAUTHORIZED) {
                    SecurityContextHolder.clearContext();
                    var session = request.getSession(false);
                    if (session != null) session.invalidate();
                }
                writer.write(request, response, ex.code());
                return;
            }
        }
        chain.doFilter(request, response);
    }
}
