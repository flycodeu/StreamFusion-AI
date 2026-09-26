package com.streamfusion.platform.common.web;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import jakarta.servlet.RequestDispatcher;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.stereotype.Component;

/** Shared MVC, security filter and servlet error output, with an explicit legacy boundary. */
@Component
public final class ApiErrorWriter {
    static final String MODULE_REQUEST_ATTRIBUTE = ApiErrorWriter.class.getName() + ".module";
    private final ObjectMapper mapper;

    public ApiErrorWriter(ObjectMapper mapper) {
        this.mapper = mapper;
    }

    public static boolean isBusinessRequest(HttpServletRequest request) {
        if (Boolean.TRUE.equals(request.getAttribute(MODULE_REQUEST_ATTRIBUTE))) return true;
        Object original = request.getAttribute(RequestDispatcher.ERROR_REQUEST_URI);
        String uri = original instanceof String value ? value : request.getRequestURI();
        String context = request.getContextPath();
        return matchesModule(uri, context + "/user")
                || matchesModule(uri, context + "/auth")
                || matchesModule(uri, context + "/menus")
                || matchesModule(uri, context + "/roles")
                || matchesModule(uri, context + "/departments");
    }

    private static boolean matchesModule(String uri, String module) {
        return uri.equals(module) || uri.startsWith(module + "/");
    }

    public ResponseEntity<Object> entity(
            HttpServletRequest request,
            int status,
            ErrorCode code,
            Object details,
            HttpHeaders headers) {
        HttpHeaders resultHeaders = new HttpHeaders();
        resultHeaders.putAll(headers);
        resultHeaders.setContentType(MediaType.APPLICATION_JSON);
        Object body;
        if (isBusinessRequest(request)) {
            body = R.error(code, details);
        } else {
            body = ApiError.fromStatus(status);
        }
        return ResponseEntity.status(status).headers(resultHeaders).body(body);
    }

    public void write(HttpServletRequest request, HttpServletResponse response, ErrorCode code)
            throws IOException {
        write(request, response, code, null);
    }

    public void write(
            HttpServletRequest request,
            HttpServletResponse response,
            ErrorCode code,
            Object details)
            throws IOException {
        if (response.isCommitted()) return;
        var result = entity(request, code.httpStatus(), code, details, new HttpHeaders());
        response.setStatus(result.getStatusCode().value());
        if (isBusinessRequest(request)) response.setHeader("Cache-Control", "no-store");
        result.getHeaders()
                .forEach(
                        (name, values) -> values.forEach(value -> response.addHeader(name, value)));
        response.setContentType(MediaType.APPLICATION_JSON_VALUE);
        response.setCharacterEncoding("UTF-8");
        if (!"HEAD".equals(request.getMethod())) {
            mapper.writeValue(response.getOutputStream(), result.getBody());
        }
    }
}
