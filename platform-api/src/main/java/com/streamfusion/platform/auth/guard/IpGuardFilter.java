package com.streamfusion.platform.auth.guard;

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
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;
import org.springframework.web.filter.OncePerRequestFilter;

/** Runs inside the dependency boundary but before Spring Session and expensive credential work. */
@Component
@Order(Ordered.HIGHEST_PRECEDENCE + 20)
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
@RequiredArgsConstructor
public class IpGuardFilter extends OncePerRequestFilter {
    private static final Set<String> RESOURCES =
            Set.of("GET /auth/csrf", "GET /auth/login/challenge", "POST /auth/login/secure");
    private static final Set<String> RESOURCE_PATHS =
            Set.of("/auth/csrf", "/auth/login/challenge", "/auth/login/secure");
    private final IpGuardProperties properties;
    private final ClientIpResolver addresses;
    private final IpBlockService blocks;
    private final LoginGuardStore store;
    private final ApiErrorWriter writer;

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        String path = request.getRequestURI().substring(request.getContextPath().length());
        String method = request.getMethod().equals("HEAD") ? "GET" : request.getMethod();
        String route = method + " " + path;
        if (!properties.enabled() || route.equals("POST /auth/logout")) {
            if (ApiErrorWriter.isBusinessRequest(request)) {
                try {
                    addresses.resolve(request);
                } catch (BusinessException invalid) {
                    // Bypassing enforcement must remain possible with malformed proxy metadata.
                    // Without a trusted result, audit records fall back to the connection address.
                    if (invalid.code() != ErrorCode.VALIDATION_ERROR) throw invalid;
                }
            }
            chain.doFilter(request, response);
            return;
        }
        if (path.contains("%")) {
            try {
                String decoded =
                        org.springframework.web.util.UriUtils.decode(
                                path, java.nio.charset.StandardCharsets.UTF_8);
                if (decoded.equals("/auth") || decoded.startsWith("/auth/")) {
                    writer.write(request, response, ErrorCode.VALIDATION_ERROR);
                    return;
                }
            } catch (IllegalArgumentException ignored) {
                writer.write(request, response, ErrorCode.VALIDATION_ERROR);
                return;
            }
        }
        if (!ApiErrorWriter.isBusinessRequest(request)) {
            chain.doFilter(request, response);
            return;
        }
        try {
            String ip = addresses.resolve(request);
            if (!route.equals("GET /auth/csrf"))
                request.setAttribute(LoginProtection.GENERATION, blocks.requireAllowed(ip));
            if (RESOURCE_PATHS.contains(path) && !RESOURCES.contains(route))
                throw BusinessException.error(ErrorCode.METHOD_NOT_ALLOWED);
            if (RESOURCES.contains(route)) {
                var counter = store.resource(ip);
                if (counter.count() > properties.resourceLimit()) {
                    response.setHeader("Retry-After", Long.toString(counter.retryAfterSeconds()));
                    throw BusinessException.error(ErrorCode.RATE_LIMITED);
                }
            }
        } catch (BusinessException error) {
            writer.write(request, response, error.code());
            return;
        }
        chain.doFilter(request, response);
    }
}
