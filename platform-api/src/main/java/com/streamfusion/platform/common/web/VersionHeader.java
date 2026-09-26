package com.streamfusion.platform.common.web;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;

/** Parses the version header used by delete operations. */
public final class VersionHeader {
    private VersionHeader() {}

    public static String require(String value) {
        if (value == null) throw BusinessException.error(ErrorCode.PRECONDITION_REQUIRED);
        if (!value.matches("\"(0|[1-9][0-9]*)\"")) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        return value.substring(1, value.length() - 1);
    }

    public static String quote(String version) {
        return "\"" + version + "\"";
    }
}
