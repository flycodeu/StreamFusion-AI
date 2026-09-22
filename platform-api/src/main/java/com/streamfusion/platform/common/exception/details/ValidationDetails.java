package com.streamfusion.platform.common.exception.details;

import java.util.List;
import java.util.regex.Pattern;

/** Never contains rejected values or framework exception messages. */
public record ValidationDetails(List<FieldError> fieldErrors) {
    public static final int MAX_FIELD_ERRORS = 20;

    public ValidationDetails {
        fieldErrors = List.copyOf(fieldErrors);
        if (fieldErrors.size() > MAX_FIELD_ERRORS)
            throw new IllegalArgumentException("Too many field errors");
    }

    public record FieldError(String field, String code, String msg) {
        private static final Pattern FIELD_NAME = Pattern.compile("[A-Za-z][A-Za-z0-9_]{0,63}");

        public FieldError {
            if (!isValidFieldName(field)) {
                throw new IllegalArgumentException("Invalid field name");
            }
            if (!"INVALID".equals(code) || !"字段格式不正确".equals(msg)) {
                throw new IllegalArgumentException("Invalid field error");
            }
        }

        public static boolean isValidFieldName(String field) {
            return field != null && FIELD_NAME.matcher(field).matches();
        }

        public static FieldError invalid(String field) {
            return new FieldError(field, "INVALID", "字段格式不正确");
        }
    }
}
