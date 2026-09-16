package com.streamfusion.platform.common.exception.details;

import java.util.List;

/** Never contains rejected values or framework exception messages. */
public record ValidationDetails(List<FieldError> fieldErrors) {
    public ValidationDetails {
        fieldErrors = List.copyOf(fieldErrors);
        if (fieldErrors.size() > 20) throw new IllegalArgumentException("Too many field errors");
    }

    public record FieldError(String field, String code, String msg) {
        public FieldError {
            if (field == null || !field.matches("[A-Za-z][A-Za-z0-9_]{0,63}")) {
                throw new IllegalArgumentException("Invalid field name");
            }
            if (!"INVALID".equals(code) || !"字段格式不正确".equals(msg)) {
                throw new IllegalArgumentException("Invalid field error");
            }
        }

        public static FieldError invalid(String field) {
            return new FieldError(field, "INVALID", "字段格式不正确");
        }
    }
}
