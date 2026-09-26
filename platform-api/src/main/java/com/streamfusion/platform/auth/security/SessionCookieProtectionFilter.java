package com.streamfusion.platform.auth.security;

import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.Cookie;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import jakarta.servlet.http.HttpServletResponseWrapper;
import java.io.IOException;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;
import org.springframework.web.filter.OncePerRequestFilter;

/** A late rejected request may revoke its own session, but must not clear a newer browser login. */
@Component
@Order(Ordered.HIGHEST_PRECEDENCE + 25)
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
public class SessionCookieProtectionFilter extends OncePerRequestFilter {
    private static final String REVOKED =
            SessionCookieProtectionFilter.class.getName() + ".revoked";

    public static void revoked(HttpServletRequest request) {
        request.setAttribute(REVOKED, Boolean.TRUE);
    }

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        chain.doFilter(
                request,
                new HttpServletResponseWrapper(response) {
                    private boolean rejected() {
                        return Boolean.TRUE.equals(request.getAttribute(REVOKED))
                                || getStatus() >= HttpServletResponse.SC_BAD_REQUEST;
                    }

                    private boolean suppress(String name, String value) {
                        return rejected()
                                && "Set-Cookie".equalsIgnoreCase(name)
                                && value != null
                                && value.startsWith("SF_SESSION=");
                    }

                    @Override
                    public void addHeader(String name, String value) {
                        if (!suppress(name, value)) super.addHeader(name, value);
                    }

                    @Override
                    public void setHeader(String name, String value) {
                        if (!suppress(name, value)) super.setHeader(name, value);
                    }

                    @Override
                    public void addCookie(Cookie cookie) {
                        if (!rejected() || !"SF_SESSION".equals(cookie.getName()))
                            super.addCookie(cookie);
                    }
                });
    }
}
