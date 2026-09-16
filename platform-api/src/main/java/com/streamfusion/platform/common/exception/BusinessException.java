package com.streamfusion.platform.common.exception;

import java.util.Objects;

/** Unchecked so a failed business operation propagates across the transaction boundary. */
public final class BusinessException extends RuntimeException {
    private final ErrorCode code;
    private final Object safeDetails;

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

    public Object safeDetails() {
        return safeDetails;
    }
}
