package com.streamfusion.platform.department.service;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.department.pojo.dto.DepartmentWriteDto;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import org.springframework.stereotype.Component;

@Component
public class DepartmentRules {
    public record Values(Long parentId, String name, int sortOrder) {}

    public Values write(DepartmentWriteDto input) {
        if (input == null) throw invalid("department");
        if (!input.isParentIdProvided()) throw invalid("parentId");
        String name = name(input.getName());
        Integer order = input.getSortOrder();
        if (order == null || order < 0 || order > 10000) throw invalid("sortOrder");
        Long parentId =
                input.getParentId() == null
                        ? null
                        : DecimalInput.id(input.getParentId(), "parentId");
        return new Values(parentId, name, order);
    }

    public String name(String value) {
        String name = value == null ? null : value.strip();
        if (name == null || name.isEmpty() || name.codePointCount(0, name.length()) > 64) {
            throw invalid("name");
        }
        return name;
    }

    public long id(String value) {
        return DecimalInput.id(value, "id");
    }

    public long version(String value) {
        return DecimalInput.version(value, "version");
    }

    public List<Long> departmentIds(List<String> values) {
        if (values == null || values.size() > 20) throw invalid("departmentIds");
        Set<Long> ids = new LinkedHashSet<>();
        for (String value : values) {
            if (!ids.add(DecimalInput.id(value, "departmentIds"))) {
                throw invalid("departmentIds");
            }
        }
        return List.copyOf(ids);
    }

    private static BusinessException invalid(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
