package com.streamfusion.platform.common.exception;

import com.streamfusion.platform.common.exception.details.ValidationDetails;

/** Stable, safe errors shared by the HTTP and security boundaries. */
public enum ErrorCode {
    VALIDATION_ERROR(400, "请检查请求参数", ValidationDetails.class),
    UNAUTHORIZED(401, "请先登录"),
    FORBIDDEN(403, "无权执行此操作"),
    CSRF_INVALID(403, "请求凭证已失效，请刷新后重试"),
    NOT_FOUND(404, "请求的资源不存在"),
    METHOD_NOT_ALLOWED(405, "请求方法不支持"),
    NOT_ACCEPTABLE(406, "响应格式不支持"),
    CONFLICT(409, "操作与当前状态冲突"),
    VERSION_CONFLICT(409, "数据已更新，请刷新后重试"),
    PRECONDITION_FAILED(412, "数据版本已变化，请刷新后重试"),
    PAYLOAD_TOO_LARGE(413, "请求内容过大"),
    UNSUPPORTED_MEDIA_TYPE(415, "请求格式不支持"),
    PRECONDITION_REQUIRED(428, "缺少数据版本"),
    RATE_LIMITED(429, "请求过于频繁，请稍后重试"),
    INTERNAL_ERROR(500, "服务内部错误，请提供请求标识以便排查"),
    DEPENDENCY_UNAVAILABLE(503, "依赖服务暂不可用，请稍后重试"),
    HTTP_ERROR(400, "请求被拒绝");

    private final int httpStatus;
    private final String message;
    private final Class<?> detailsType;

    ErrorCode(int httpStatus, String message) {
        this(httpStatus, message, null);
    }

    ErrorCode(int httpStatus, String message, Class<?> detailsType) {
        this.httpStatus = httpStatus;
        this.message = message;
        this.detailsType = detailsType;
    }

    public int httpStatus() {
        return httpStatus;
    }

    public String message() {
        return message;
    }

    public void validateDetails(Object details) {
        if (details != null && (detailsType == null || details.getClass() != detailsType)) {
            throw new IllegalArgumentException("Unsupported error details");
        }
    }

    public static ErrorCode fromStatus(int status) {
        // Several business errors share a status; the default must not depend on enum order.
        return switch (status) {
            case 400, 422 -> VALIDATION_ERROR;
            case 401 -> UNAUTHORIZED;
            case 403 -> FORBIDDEN;
            case 404 -> NOT_FOUND;
            case 405 -> METHOD_NOT_ALLOWED;
            case 406 -> NOT_ACCEPTABLE;
            case 409 -> CONFLICT;
            case 412 -> PRECONDITION_FAILED;
            case 413 -> PAYLOAD_TOO_LARGE;
            case 415 -> UNSUPPORTED_MEDIA_TYPE;
            case 428 -> PRECONDITION_REQUIRED;
            case 429 -> RATE_LIMITED;
            case 503 -> DEPENDENCY_UNAVAILABLE;
            default -> status >= 500 ? INTERNAL_ERROR : HTTP_ERROR;
        };
    }
}
