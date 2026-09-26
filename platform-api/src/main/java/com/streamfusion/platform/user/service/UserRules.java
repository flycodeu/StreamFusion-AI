package com.streamfusion.platform.user.service;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.user.pojo.enums.UserGender;
import java.util.List;
import java.util.Set;
import java.util.regex.Pattern;
import org.springframework.stereotype.Component;

/** Shared request rules for the account and personal profile entry points. */
@Component
public class UserRules {
    private static final Pattern USERNAME = Pattern.compile("[A-Za-z0-9]{4,32}");
    private static final Pattern PHONE = Pattern.compile("[+]?[0-9]{7,20}");
    private static final Pattern EMAIL =
            Pattern.compile(
                    "[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@"
                            + "[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?"
                            + "(?:[.][A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+");
    private static final Set<String> AVATARS =
            Set.of(
                    "default",
                    "avatar-01",
                    "avatar-02",
                    "avatar-03",
                    "avatar-04",
                    "avatar-05",
                    "avatar-06");

    public String username(String value) {
        if (value == null || !USERNAME.matcher(value).matches()) {
            throw invalid("username");
        }
        return value;
    }

    public String nickname(String value) {
        String normalized = optional(value);
        if (normalized != null && normalized.codePointCount(0, normalized.length()) > 64) {
            throw invalid("nickname");
        }
        return normalized;
    }

    public String avatarKey(String value) {
        String normalized = optional(value);
        if (normalized != null && !AVATARS.contains(normalized)) {
            throw invalid("avatarKey");
        }
        return normalized;
    }

    public String phone(String value) {
        String normalized = optional(value);
        if (normalized != null && !PHONE.matcher(normalized).matches()) {
            throw invalid("phone");
        }
        return normalized;
    }

    public String email(String value) {
        String normalized = optional(value);
        if (normalized == null) {
            return null;
        }
        int separator = normalized.indexOf('@');
        if (normalized.length() > 254
                || separator < 1
                || separator > 64
                || !EMAIL.matcher(normalized).matches()
                || normalized.startsWith(".")
                || normalized.charAt(separator - 1) == '.'
                || normalized.contains("..")) {
            throw invalid("email");
        }
        return normalized;
    }

    public int gender(Integer value) {
        if (value == null) {
            return UserGender.UNKNOWN.getCode();
        }
        if (!UserGender.isValid(value)) {
            throw invalid("gender");
        }
        return value;
    }

    public long version(String value) {
        return DecimalInput.version(value, "version");
    }

    public long id(String value) {
        return DecimalInput.id(value, "id");
    }

    private String optional(String value) {
        if (value == null) {
            return null;
        }
        String normalized = value.strip();
        return normalized.isEmpty() ? null : normalized;
    }

    private BusinessException invalid(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
