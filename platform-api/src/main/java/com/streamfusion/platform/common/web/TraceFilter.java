package com.streamfusion.platform.common.web;

import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.regex.Pattern;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;
import org.springframework.web.filter.OncePerRequestFilter;
import org.springframework.web.servlet.HandlerMapping;

@Component
@Order(Ordered.HIGHEST_PRECEDENCE)
public class TraceFilter extends OncePerRequestFilter {
    private static final Logger LOG = LoggerFactory.getLogger(TraceFilter.class);
    private static final Pattern VALID = Pattern.compile("^[a-f0-9]{32}$");

    @Override
    protected boolean shouldNotFilterErrorDispatch() {
        return false;
    }

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        String previous = MDC.get("traceId");
        String id = (String) request.getAttribute("traceId");
        if (id == null) {
            String incoming = request.getHeader("X-Trace-Id");
            id =
                    incoming != null && VALID.matcher(incoming).matches()
                            ? incoming
                            : UUID.randomUUID().toString().replace("-", "");
            request.setAttribute("traceId", id);
        }
        MDC.put("traceId", id);
        response.setHeader("X-Trace-Id", id);
        if (ApiErrorWriter.isBusinessRequest(request))
            response.setHeader("Cache-Control", "no-store");
        long start = System.nanoTime();
        try {
            chain.doFilter(request, response);
        } finally {
            Object route = request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE);
            LOG.info(
                    "method={} route={} status={} durationMs={}",
                    request.getMethod(),
                    route == null ? "<unmatched>" : route,
                    response.getStatus(),
                    TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start));
            if (previous == null) {
                MDC.remove("traceId");
            } else {
                MDC.put("traceId", previous);
            }
        }
    }
}
