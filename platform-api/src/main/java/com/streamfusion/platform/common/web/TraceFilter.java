package com.streamfusion.platform.common.web;

import com.streamfusion.platform.common.security.ModuleRegistry;
import com.streamfusion.platform.monitor.diagnostics.DiagnosticLog;
import jakarta.servlet.AsyncEvent;
import jakarta.servlet.AsyncListener;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
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
    private final ModuleRegistry modules;
    private final DiagnosticLog diagnostics;

    public TraceFilter(ModuleRegistry modules, DiagnosticLog diagnostics) {
        this.modules = modules;
        this.diagnostics = diagnostics;
    }

    @Override
    protected boolean shouldNotFilterErrorDispatch() {
        return false;
    }

    @Override
    protected boolean shouldNotFilterAsyncDispatch() {
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
        if (modules.matchesRequest(request))
            request.setAttribute(ApiErrorWriter.MODULE_REQUEST_ATTRIBUTE, Boolean.TRUE);
        if (ApiErrorWriter.isBusinessRequest(request))
            response.setHeader("Cache-Control", "no-store");
        String marker = TraceFilter.class.getName();
        var completion = (Completion) request.getAttribute(marker);
        if (completion == null) {
            completion = new Completion(request, response);
            request.setAttribute(marker, completion);
        }
        try {
            chain.doFilter(request, response);
        } catch (ServletException | IOException | RuntimeException ex) {
            request.setAttribute(DiagnosticLog.EXCEPTION, ex);
            completion.failed = true;
            throw ex;
        } finally {
            if (request.isAsyncStarted()) {
                if (completion.listening.compareAndSet(false, true)) {
                    try {
                        request.getAsyncContext().addListener(completion);
                    } catch (IllegalStateException ex) {
                        completion.finish();
                    }
                }
            } else completion.finish();
            if (previous == null) {
                MDC.remove("traceId");
            } else {
                MDC.put("traceId", previous);
            }
        }
    }

    private final class Completion implements AsyncListener {
        private final HttpServletRequest request;
        private final HttpServletResponse response;
        private final long start = System.nanoTime();
        private final AtomicBoolean written = new AtomicBoolean();
        private final AtomicBoolean listening = new AtomicBoolean();
        private volatile boolean failed;

        Completion(HttpServletRequest request, HttpServletResponse response) {
            this.request = request;
            this.response = response;
        }

        void finish() {
            if (!written.compareAndSet(false, true)) return;
            long duration = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start);
            int status = failed ? Math.max(500, response.getStatus()) : response.getStatus();
            Object route = request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE);
            LOG.info(
                    "traceId={} method={} route={} status={} durationMs={}",
                    request.getAttribute("traceId"),
                    request.getMethod(),
                    route == null ? "<unmatched>" : route,
                    status,
                    duration);
            diagnostics.request(request, status, duration);
        }

        @Override
        public void onComplete(AsyncEvent event) {
            finish();
        }

        @Override
        public void onTimeout(AsyncEvent event) {
            failed = true;
        }

        @Override
        public void onError(AsyncEvent event) {
            failed = true;
            request.setAttribute(DiagnosticLog.EXCEPTION, event.getThrowable());
        }

        @Override
        public void onStartAsync(AsyncEvent event) {
            event.getAsyncContext().addListener(this);
        }
    }
}
