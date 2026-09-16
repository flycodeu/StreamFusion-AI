package com.streamfusion.platform.common.web;

import java.time.Instant;
import org.slf4j.MDC;

public record ApiError(
        String code, String message, Object data, String traceId, Instant timestamp) {
    public static ApiError fromStatus(int status) {
        String code =
                switch (status) {
                    case 400, 422 -> "VALIDATION_ERROR";
                    case 401 -> "UNAUTHORIZED";
                    case 403 -> "FORBIDDEN";
                    case 404 -> "NOT_FOUND";
                    case 405 -> "METHOD_NOT_ALLOWED";
                    case 409 -> "CONFLICT";
                    case 415 -> "UNSUPPORTED_MEDIA_TYPE";
                    case 429 -> "RATE_LIMITED";
                    default -> status >= 500 ? "INTERNAL_ERROR" : "HTTP_ERROR";
                };
        String message = status >= 500 ? "Internal server error" : "Request rejected";
        return new ApiError(code, message, null, MDC.get("traceId"), Instant.now());
    }
}
