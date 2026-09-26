package com.streamfusion.platform.menu.service;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import com.streamfusion.platform.common.security.ModuleRegistry;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.menu.pojo.dto.MenuTreeQueryDto;
import com.streamfusion.platform.menu.pojo.dto.MenuWriteDto;
import java.util.List;
import java.util.Set;
import java.util.regex.Pattern;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Validates local page paths and module declarations registered by the backend. */
@Component
@RequiredArgsConstructor
public class MenuRules {
    private static final Pattern ROUTE = Pattern.compile("[A-Za-z][A-Za-z0-9_:-]{0,63}");
    private static final Pattern PATH = Pattern.compile("/[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*");
    private static final Pattern LEGACY_COMPONENT = Pattern.compile("[A-Z][A-Z0-9_]{0,63}");
    private static final Pattern ICON = Pattern.compile("[a-z][a-z0-9-]{0,63}");
    private static final Set<String> RESERVED_PATHS =
            Set.of(
                    "/login",
                    "/home",
                    "/profile",
                    "/change-password",
                    "/forbidden",
                    "/unavailable",
                    "/api",
                    "/auth",
                    "/actuator",
                    "/assets",
                    "/src",
                    "/public",
                    "/node_modules");
    private final ModuleRegistry modules;

    public record Values(
            Long parentId,
            String name,
            String type,
            String routeName,
            String path,
            String componentKey,
            String moduleKey,
            String icon,
            int sortOrder,
            boolean visible,
            boolean enabled) {}

    public Values write(MenuWriteDto input) {
        if (input == null) throw invalid("menu");
        String type = input.getType();
        if (!"DIRECTORY".equals(type) && !"PAGE".equals(type)) throw invalid("type");
        String name = input.getName() == null ? null : input.getName().strip();
        if (name == null || name.isEmpty() || name.codePointCount(0, name.length()) > 64) {
            throw invalid("name");
        }
        if (input.getSortOrder() == null
                || input.getSortOrder() < 0
                || input.getSortOrder() > 10000) throw invalid("sortOrder");
        if (input.getVisible() == null) throw invalid("visible");
        if (input.getEnabled() == null) throw invalid("enabled");
        String icon = blankToNull(input.getIcon());
        if (icon != null && !ICON.matcher(icon).matches()) throw invalid("icon");
        Long parentId =
                input.getParentId() == null
                        ? null
                        : DecimalInput.id(input.getParentId(), "parentId");
        if ("DIRECTORY".equals(type)) {
            if (input.getRouteName() != null
                    || input.getPath() != null
                    || input.getComponentKey() != null
                    || input.getModuleKey() != null) throw invalid("type");
            return new Values(
                    parentId,
                    name,
                    type,
                    null,
                    null,
                    null,
                    null,
                    icon,
                    input.getSortOrder(),
                    input.getVisible(),
                    input.getEnabled());
        }
        String routeName = input.getRouteName();
        String path = input.getPath();
        if (routeName == null || !ROUTE.matcher(routeName).matches()) throw invalid("routeName");
        if (path == null
                || path.length() > 200
                || !PATH.matcher(path).matches()
                || RESERVED_PATHS.stream()
                        .anyMatch(
                                root ->
                                        path.equalsIgnoreCase(root)
                                                || path.regionMatches(
                                                        true,
                                                        0,
                                                        root + "/",
                                                        0,
                                                        root.length() + 1))) {
            throw invalid("path");
        }
        String componentKey = blankToNull(input.getComponentKey());
        if (componentKey == null) componentKey = path;
        else if (!LEGACY_COMPONENT.matcher(componentKey).matches() && !componentKey.startsWith("/"))
            componentKey = "/" + componentKey;
        if (componentKey.length() > 64
                || !(LEGACY_COMPONENT.matcher(componentKey).matches()
                        || PATH.matcher(componentKey).matches())) {
            throw invalid("componentKey");
        }
        String moduleKey = blankToNull(input.getModuleKey());
        if (moduleKey == null) moduleKey = routeName;
        if (!ROUTE.matcher(moduleKey).matches() || !modules.contains(moduleKey))
            throw invalid("moduleKey");
        return new Values(
                parentId,
                name,
                type,
                routeName,
                path,
                componentKey,
                moduleKey,
                icon,
                input.getSortOrder(),
                input.getVisible(),
                input.getEnabled());
    }

    public MenuTreeQueryDto query(MenuTreeQueryDto input) {
        if (input == null) return new MenuTreeQueryDto();
        if (input.getName() != null) {
            String name = input.getName().strip();
            if (name.codePointCount(0, name.length()) > 64) throw invalid("name");
            input.setName(name.isEmpty() ? null : name);
        }
        if (input.getType() != null
                && !"DIRECTORY".equals(input.getType())
                && !"PAGE".equals(input.getType())) throw invalid("type");
        return input;
    }

    public long id(String value) {
        return DecimalInput.id(value, "id");
    }

    public long version(String value) {
        return DecimalInput.version(value, "version");
    }

    private static String blankToNull(String value) {
        if (value == null) return null;
        String trimmed = value.strip();
        return trimmed.isEmpty() ? null : trimmed;
    }

    private static BusinessException invalid(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
