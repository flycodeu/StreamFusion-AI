package com.streamfusion.platform.common.web;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import org.slf4j.MDC;

@Schema(description = "非业务路径错误响应")
public record ApiError(
        @Schema(description = "错误编码") String code,
        @Schema(description = "错误说明") String message,
        @Schema(description = "错误数据") Object data,
        @Schema(description = "跟踪ID") String traceId,
        @Schema(description = "响应时间") Instant timestamp) {
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
