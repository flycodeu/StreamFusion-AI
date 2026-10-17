package com.streamfusion.platform.camera.service;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;

/** Shared input, bounded-collection and formatting rules; no persistence or authorization state. */
final class CameraGroupRules {
    private CameraGroupRules() {}

    static long id(String value) {
        return DecimalInput.id(value, "id");
    }

    static long version(String value) {
        return DecimalInput.version(value, "version");
    }

    static Long optionalId(String value) {
        return value == null ? null : id(value);
    }

    static String str(Object value) {
        return value == null ? "" : value.toString();
    }

    static String strOrNull(Object value) {
        return value == null ? null : value.toString();
    }

    static String text(String value, int max, boolean required) {
        String s = CameraSourceRules.text(value, max, required);
        return s == null || s.isEmpty() ? null : s;
    }

    static int order(Integer value) {
        if (value == null) return 0;
        if (value < 0) throw invalid();
        return value;
    }

    static void pageBounds(int page, int size) {
        if (page < 1 || size < 1 || size > 100) throw invalid();
    }

    static void bounded(int count) {
        if (count > 10000) throw conflict();
    }

    static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }

    static BusinessException conflict() {
        return BusinessException.error(ErrorCode.CONFLICT);
    }
}
