package com.streamfusion.platform.auth.security;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import com.streamfusion.platform.loginrecord.service.LoginRecordService;
import com.streamfusion.platform.user.service.UserService;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import lombok.RequiredArgsConstructor;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.http.MediaType;
import org.springframework.security.core.context.SecurityContext;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.session.Session;
import org.springframework.session.SessionRepository;
import org.springframework.session.web.http.CookieSerializer;
import org.springframework.stereotype.Component;
import org.springframework.web.filter.OncePerRequestFilter;

/**
 * Reads a session snapshot after IP enforcement but before Spring Session. Polling must never
 * access its servlet wrapper: even reading authentication through that wrapper renews idle time.
 */
@Component
@Order(Ordered.HIGHEST_PRECEDENCE + 30)
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
@RequiredArgsConstructor
public class SessionStatusFilter extends OncePerRequestFilter {
    private final ObjectProvider<SessionRepository<? extends Session>> repositories;
    private final CookieSerializer cookies;
    private final CurrentUserService current;
    private final UserService users;
    private final LoginRecordService records;
    private final ApiErrorWriter errors;
    private final ObjectMapper json;

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        String path = request.getRequestURI().substring(request.getContextPath().length());
        if (!"/auth/session".equals(path)) {
            chain.doFilter(request, response);
            return;
        }
        response.setHeader("Cache-Control", "no-store");
        response.setHeader("X-Content-Type-Options", "nosniff");
        if (!"GET".equals(request.getMethod()) && !"HEAD".equals(request.getMethod())) {
            errors.write(request, response, ErrorCode.METHOD_NOT_ALLOWED);
            return;
        }
        try {
            Snapshot snapshot = read(request);
            var authentication =
                    snapshot.context() == null ? null : snapshot.context().getAuthentication();
            if (authentication == null
                    || !authentication.isAuthenticated()
                    || !(authentication.getPrincipal() instanceof SessionPrincipalDto principal)) {
                throw BusinessException.error(ErrorCode.UNAUTHORIZED);
            }
            try {
                current.validate(principal, users.getById(principal.getUserId()));
            } catch (BusinessException error) {
                if (error.code() != ErrorCode.UNAUTHORIZED) throw error;
                throw records.endedSession(principal, snapshot.activity());
            }
            response.setStatus(HttpServletResponse.SC_OK);
            response.setContentType(MediaType.APPLICATION_JSON_VALUE);
            response.setCharacterEncoding("UTF-8");
            if (!"HEAD".equals(request.getMethod())) {
                json.writeValue(response.getOutputStream(), R.success());
            }
        } catch (BusinessException error) {
            errors.write(request, response, error.code(), error.safeDetails());
        }
    }

    private Snapshot read(HttpServletRequest request) {
        var repository = repositories.getIfAvailable();
        String contextKey = HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY;
        if (repository != null) {
            var ids = cookies.readCookieValues(request);
            // Ambiguous session cookies cannot select another identity for the status response.
            if (ids.size() != 1) return new Snapshot(null, null);
            Session session = repository.findById(ids.getFirst());
            if (session == null || session.isExpired()) return new Snapshot(null, null);
            return snapshot(
                    session.getAttribute(contextKey),
                    session.getAttribute(LoginRecordService.SESSION_ATTRIBUTE));
        }
        // Servlet-only test contexts have no Spring Session repository.
        var session = request.getSession(false);
        return session == null
                ? new Snapshot(null, null)
                : snapshot(
                        session.getAttribute(contextKey),
                        session.getAttribute(LoginRecordService.SESSION_ATTRIBUTE));
    }

    private static Snapshot snapshot(Object context, Object activity) {
        return new Snapshot(
                context instanceof SecurityContext value ? value : null,
                activity instanceof LoginRecordService.Activity value ? value : null);
    }

    private record Snapshot(SecurityContext context, LoginRecordService.Activity activity) {}
}
