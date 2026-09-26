package com.streamfusion.platform.role.service;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.role.pojo.dto.RoleQueryDto;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import java.util.regex.Pattern;
import org.springframework.stereotype.Component;

@Component
public class RoleRules {
    private static final Pattern CODE = Pattern.compile("[A-Z][A-Z0-9_]{1,63}");

    public RoleQueryDto query(RoleQueryDto input) {
        if (input == null) throw invalid("query");
        if (input.getKeyword() != null) {
            String keyword = input.getKeyword().strip();
            if (keyword.codePointCount(0, keyword.length()) > 64) throw invalid("keyword");
            input.setKeyword(keyword.isEmpty() ? null : keyword);
        }
        if (input.getStatus() != null
                && !Set.of("ENABLED", "DISABLED").contains(input.getStatus())) {
            throw invalid("status");
        }
        return input;
    }

    public String code(String value) {
        if (value == null || !CODE.matcher(value).matches() || "SUPER_ADMIN".equals(value)) {
            throw invalid("code");
        }
        return value;
    }

    public String name(String value) {
        String result = value == null ? null : value.strip();
        if (result == null || result.isEmpty() || result.codePointCount(0, result.length()) > 64) {
            throw invalid("name");
        }
        return result;
    }

    public String description(String value) {
        if (value == null) return null;
        String result = value.strip();
        if (result.codePointCount(0, result.length()) > 255) throw invalid("description");
        return result.isEmpty() ? null : result;
    }

    public long id(String value) {
        return DecimalInput.id(value, "id");
    }

    public long version(String value) {
        return DecimalInput.version(value, "version");
    }

    public List<Long> ids(List<String> values, String field) {
        if (values == null || values.size() > 1000) throw invalid(field);
        Set<Long> result = new LinkedHashSet<>();
        for (String value : values) {
            long id = DecimalInput.id(value, field);
            if (!result.add(id)) throw invalid(field);
        }
        return List.copyOf(result);
    }

    private static BusinessException invalid(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
