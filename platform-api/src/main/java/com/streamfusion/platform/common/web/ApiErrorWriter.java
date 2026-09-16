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
    private final ObjectMapper mapper;

    public ApiErrorWriter(ObjectMapper mapper) {
        this.mapper = mapper;
    }

    public static boolean isBusinessRequest(HttpServletRequest request) {
        Object original = request.getAttribute(RequestDispatcher.ERROR_REQUEST_URI);
        String uri = original instanceof String value ? value : request.getRequestURI();
        String prefix = request.getContextPath() + "/api/v1";
        return uri.equals(prefix) || uri.startsWith(prefix + "/");
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
        if (response.isCommitted()) return;
        var result = entity(request, code.httpStatus(), code, null, new HttpHeaders());
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
