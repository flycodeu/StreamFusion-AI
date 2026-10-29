package com.streamfusion.platform.inspection.evidence;

import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.authority.SimpleGrantedAuthority;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.filter.OncePerRequestFilter;

/** A node identity is established only from the deployment credential over HTTPS. */
public final class InspectionNodeAuthentication extends OncePerRequestFilter {
    public static final String NODE_ID = InspectionNodeAuthentication.class.getName() + ".nodeId";
    private final InspectionEvidenceProperties properties;
    private final ApiErrorWriter errors;

    public InspectionNodeAuthentication(
            InspectionEvidenceProperties properties, ApiErrorWriter errors) {
        this.properties = properties;
        this.errors = errors;
    }

    @Override
    protected boolean shouldNotFilter(HttpServletRequest request) {
        String path = request.getRequestURI().substring(request.getContextPath().length());
        return !(path.equals("/inspection-nodes/evidence/authorize")
                || path.equals("/inspection-nodes/evidence/complete"));
    }

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        String header = request.getHeader("Authorization");
        if (request.getContentLengthLong() > 8192
                || request.getHeader("Transfer-Encoding") != null) {
            errors.write(request, response, ErrorCode.PAYLOAD_TOO_LARGE);
            return;
        }
        if (!request.isSecure() || header == null || !header.startsWith("Bearer ")) {
            errors.write(request, response, ErrorCode.UNAUTHORIZED);
            return;
        }
        String presented = header.substring(7);
        if (presented.length() < 32
                || presented.length() > 256
                || !presented.chars().allMatch(c -> c >= 33 && c <= 126)) {
            errors.write(request, response, ErrorCode.UNAUTHORIZED);
            return;
        }
        String nodeId = null;
        for (var entry : properties.getNodeTokens().entrySet()) {
            if (MessageDigest.isEqual(
                    entry.getValue().getBytes(StandardCharsets.US_ASCII),
                    presented.getBytes(StandardCharsets.US_ASCII))) nodeId = entry.getKey();
        }
        if (nodeId == null) {
            errors.write(request, response, ErrorCode.UNAUTHORIZED);
            return;
        }
        var context = SecurityContextHolder.createEmptyContext();
        context.setAuthentication(
                UsernamePasswordAuthenticationToken.authenticated(
                        nodeId,
                        null,
                        java.util.List.of(new SimpleGrantedAuthority("ROLE_INSPECTION_NODE"))));
        SecurityContextHolder.setContext(context);
        request.setAttribute(NODE_ID, nodeId);
        try {
            chain.doFilter(request, response);
        } finally {
            SecurityContextHolder.clearContext();
        }
    }
}
