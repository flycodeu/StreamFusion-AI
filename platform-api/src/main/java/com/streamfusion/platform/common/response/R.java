package com.streamfusion.platform.common.response;

import com.fasterxml.jackson.annotation.JsonInclude;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.time.Instant;
import java.util.Objects;
import org.slf4j.MDC;

/**
 * JSON body only. HTTP status and headers remain the controller/error boundary's responsibility.
 */
@JsonInclude(JsonInclude.Include.ALWAYS)
public record R<T>(String code, String msg, T data, String traceId, Instant timestamp) {
    public R {
        Objects.requireNonNull(timestamp);
        if (traceId == null || !traceId.matches("[a-f0-9]{32}")) {
            throw new IllegalStateException("Response requires a request trace");
        }
        if ("SUCCESS".equals(code)) {
            if (!"操作成功".equals(msg) || data instanceof R<?>) {
                throw new IllegalArgumentException("Invalid success response");
            }
        } else {
            ErrorCode error = ErrorCode.valueOf(code);
            if (!error.message().equals(msg))
                throw new IllegalArgumentException("Unsafe error message");
            error.validateDetails(data);
        }
    }

    public static <T> R<T> success(T data) {
        return new R<>("SUCCESS", "操作成功", data, MDC.get("traceId"), Instant.now());
    }

    public static R<Void> success() {
        return success(null);
    }

    public static R<Void> error(ErrorCode code) {
        return error(code, null);
    }

    public static <D> R<D> error(ErrorCode code, D details) {
        return new R<>(code.name(), code.message(), details, MDC.get("traceId"), Instant.now());
    }
}
