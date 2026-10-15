package com.streamfusion.platform.common.exception;

import java.util.Objects;

/** Unchecked so a failed business operation propagates across the transaction boundary. */
public final class BusinessException extends RuntimeException {
    private final ErrorCode code;
    private final Object safeDetails;
    private String diagnosticReason;

    private BusinessException(ErrorCode code, Object safeDetails) {
        super(Objects.requireNonNull(code).message());
        code.validateDetails(safeDetails);
        this.code = code;
        this.safeDetails = safeDetails;
    }

    public static BusinessException error(ErrorCode code) {
        return new BusinessException(code, null);
    }

    public static BusinessException error(ErrorCode code, Object safeDetails) {
        return new BusinessException(code, safeDetails);
    }

    public ErrorCode code() {
        return code;
    }

    public BusinessException diagnosed(String reason) {
        if (reason == null || !reason.matches("[A-Z][A-Z0-9_]{0,79}"))
            throw new IllegalArgumentException("Invalid diagnostic reason");
        this.diagnosticReason = reason;
        return this;
    }

    public String diagnosticReason() {
        return diagnosticReason;
    }

    public Object safeDetails() {
        return safeDetails;
    }
}
