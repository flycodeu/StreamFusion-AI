package com.streamfusion.platform.monitor.diagnostics;

import com.fasterxml.jackson.databind.JsonNode;
import jakarta.servlet.http.*;
import java.io.*;
import java.lang.reflect.Type;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.MethodParameter;
import org.springframework.http.*;
import org.springframework.http.converter.HttpMessageConverter;
import org.springframework.web.bind.annotation.ControllerAdvice;
import org.springframework.web.context.request.*;
import org.springframework.web.method.HandlerMethod;
import org.springframework.web.servlet.*;
import org.springframework.web.servlet.config.annotation.*;
import org.springframework.web.servlet.mvc.method.annotation.*;

/**
 * Advice observes ordinary JSON DTOs. Streaming responses and business return values are untouched.
 */
@ControllerAdvice
@RequiredArgsConstructor
public class DiagnosticPayloadAdvice extends RequestBodyAdviceAdapter
        implements ResponseBodyAdvice<Object> {
    static final String ATTRIBUTE = DiagnosticPayloadAdvice.class.getName();
    private final PayloadSanitizer sanitizer;

    static final class Capture {
        final DiagnosticCaptureService.Window window;
        final JsonNode parameters;
        final ByteArrayOutputStream raw = new ByteArrayOutputStream();
        JsonNode request;
        JsonNode response;
        String requestState = "NOT_READ";
        boolean overflow;

        Capture(DiagnosticCaptureService.Window window, JsonNode parameters) {
            this.window = window;
            this.parameters = parameters;
        }
    }

    private static Capture capture() {
        var attrs = RequestContextHolder.getRequestAttributes();
        return attrs instanceof ServletRequestAttributes s
                        && s.getRequest().getAttribute(ATTRIBUTE) instanceof Capture c
                ? c
                : null;
    }

    @Override
    public boolean supports(
            MethodParameter p, Type t, Class<? extends HttpMessageConverter<?>> converter) {
        return capture() != null;
    }

    @Override
    public HttpInputMessage beforeBodyRead(
            HttpInputMessage input,
            MethodParameter p,
            Type type,
            Class<? extends HttpMessageConverter<?>> converter)
            throws IOException {
        var c = capture();
        if (c == null) return input;
        return new HttpInputMessage() {
            @Override
            public HttpHeaders getHeaders() {
                return input.getHeaders();
            }

            @Override
            public InputStream getBody() throws IOException {
                return new FilterInputStream(input.getBody()) {
                    void remember(byte[] bytes, int offset, int length) {
                        int accepted = Math.min(length, 65536 - c.raw.size());
                        if (accepted > 0) c.raw.write(bytes, offset, accepted);
                        if (accepted < length) c.overflow = true;
                    }

                    @Override
                    public int read() throws IOException {
                        int value = in.read();
                        if (value >= 0) remember(new byte[] {(byte) value}, 0, 1);
                        return value;
                    }

                    @Override
                    public int read(byte[] bytes, int offset, int length) throws IOException {
                        int n = in.read(bytes, offset, length);
                        if (n > 0) remember(bytes, offset, n);
                        return n;
                    }
                };
            }
        };
    }

    @Override
    public Object afterBodyRead(
            Object body,
            HttpInputMessage input,
            MethodParameter p,
            Type type,
            Class<? extends HttpMessageConverter<?>> converter) {
        var c = capture();
        if (c != null) {
            c.request = sanitizer.project(body);
            c.requestState = "CAPTURED";
            c.raw.reset();
        }
        return body;
    }

    @Override
    public boolean supports(
            MethodParameter returnType, Class<? extends HttpMessageConverter<?>> converter) {
        return capture() != null;
    }

    @Override
    public Object beforeBodyWrite(
            Object body,
            MethodParameter p,
            MediaType media,
            Class<? extends HttpMessageConverter<?>> converter,
            org.springframework.http.server.ServerHttpRequest request,
            org.springframework.http.server.ServerHttpResponse response) {
        var c = capture();
        if (c != null && MediaType.APPLICATION_JSON.isCompatibleWith(media))
            c.response = sanitizer.project(body);
        return body;
    }

    public JsonNode requestDetails(HttpServletRequest request, int status) {
        if (!(request.getAttribute(ATTRIBUTE) instanceof Capture c)
                || c.window.errorsOnly && status < 400) return null;
        if (c.request == null && c.raw.size() > 0) {
            if (c.overflow) c.requestState = "TOO_LARGE";
            else
                try {
                    c.request = sanitizer.project(sanitizer.parse(c.raw.toByteArray()));
                    c.requestState = "CAPTURED";
                } catch (Exception ex) {
                    c.requestState = "INVALID_JSON";
                }
        }
        c.window.recorded.incrementAndGet();
        var node = com.fasterxml.jackson.databind.node.JsonNodeFactory.instance.objectNode();
        node.put("state", c.requestState);
        node.set("parameters", c.parameters);
        if (c.request != null) node.set("body", c.request);
        return node;
    }

    public JsonNode responseDetails(HttpServletRequest request, int status) {
        return request.getAttribute(ATTRIBUTE) instanceof Capture c
                        && (!c.window.errorsOnly || status >= 400)
                ? c.response
                : null;
    }

    @Configuration
    @RequiredArgsConstructor
    public static class PayloadWebConfiguration implements WebMvcConfigurer {
        private final DiagnosticCaptureService windows;
        private final PayloadSanitizer sanitizer;

        @Override
        public void addInterceptors(InterceptorRegistry registry) {
            registry.addInterceptor(
                    new HandlerInterceptor() {
                        @Override
                        public boolean preHandle(
                                HttpServletRequest request,
                                HttpServletResponse response,
                                Object handler) {
                            if (!(handler instanceof HandlerMethod method)) return true;
                            String content = request.getContentType();
                            if (content != null
                                    && !content.toLowerCase(Locale.ROOT)
                                            .startsWith("application/json")) return true;
                            Object route =
                                    request.getAttribute(
                                            HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE);
                            if (!(route instanceof String pattern)) return true;
                            try {
                                var window = windows.match(request, method, pattern);
                                if (window == null) return true;
                                var query = new LinkedHashMap<String, Object>();
                                String raw = request.getQueryString();
                                if (raw != null && raw.length() <= 4096) {
                                    for (String pair : raw.split("&", 51)) {
                                        String[] parts = pair.split("=", 2);
                                        if (query.size() >= 50) break;
                                        query.put(
                                                URLDecoder.decode(parts[0], StandardCharsets.UTF_8),
                                                parts.length == 2
                                                        ? URLDecoder.decode(
                                                                parts[1], StandardCharsets.UTF_8)
                                                        : "");
                                    }
                                }
                                Object path =
                                        request.getAttribute(
                                                HandlerMapping.URI_TEMPLATE_VARIABLES_ATTRIBUTE);
                                request.setAttribute(
                                        ATTRIBUTE,
                                        new Capture(
                                                window,
                                                sanitizer.project(
                                                        Map.of(
                                                                "query",
                                                                query,
                                                                "path",
                                                                path == null ? Map.of() : path))));
                            } catch (RuntimeException ignored) {
                            }
                            return true;
                        }
                    });
        }
    }
}
