package com.streamfusion.platform.common.validation;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import java.util.List;
import java.util.regex.Pattern;

/** Parses decimal IDs and edit versions used by API requests. */
public final class DecimalInput {
    private static final Pattern DECIMAL = Pattern.compile("[0-9]+");

    private DecimalInput() {}

    public static long id(String value, String field) {
        return parse(value, true, field);
    }

    public static long version(String value, String field) {
        return parse(value, false, field);
    }

    private static long parse(String value, boolean positive, String field) {
        if (value == null || value.length() > 19 || !DECIMAL.matcher(value).matches()) {
            throw invalid(field);
        }
        try {
            long parsed = Long.parseLong(value);
            if (positive && parsed == 0) throw invalid(field);
            return parsed;
        } catch (NumberFormatException ignored) {
            throw invalid(field);
        }
    }

    private static BusinessException invalid(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
