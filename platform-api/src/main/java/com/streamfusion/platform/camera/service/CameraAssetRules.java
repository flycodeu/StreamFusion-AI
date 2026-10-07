package com.streamfusion.platform.camera.service;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import java.util.List;
import java.util.Set;

/** Small asset-specific rules; shared ID, version, HTTP and authorization rules stay shared. */
public final class CameraAssetRules {
    private static final Set<String> USAGES = Set.of("MAIN", "SUB", "THIRD", "CUSTOM", "UNKNOWN");

    private CameraAssetRules() {}

    public static String text(String input, int max, String field, boolean nullable) {
        if (input == null) {
            if (nullable) return null;
            throw invalid(field);
        }
        String value = input.strip();
        if (value.codePointCount(0, value.length()) > max || value.indexOf('\0') >= 0)
            throw invalid(field);
        if (value.isEmpty()) {
            if (nullable) return null;
            throw invalid(field);
        }
        return value;
    }

    public static String usage(String value) {
        if (value == null || !USAGES.contains(value)) throw invalid("usageHint");
        return value;
    }

    public static boolean enabled(Boolean value) {
        if (value == null) throw invalid("enabled");
        return value;
    }

    public static void rtsp(String kind) {
        if (!"RTSP".equals(kind)) throw invalid("locatorKind");
    }

    public static BusinessException invalid(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
