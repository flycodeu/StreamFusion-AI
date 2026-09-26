package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.security.ModuleRegistry;
import com.streamfusion.platform.menu.pojo.dto.MenuWriteDto;
import com.streamfusion.platform.menu.service.MenuRules;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class MenuRulesTest {
    private final ModuleRegistry modules = mock(ModuleRegistry.class);
    private final MenuRules rules = new MenuRules(modules);

    @Test
    void pagePathAndUniqueKeyProvideDefaultFileAndModuleMappings() {
        var input = page();
        when(modules.contains("camera")).thenReturn(true);
        var values = rules.write(input);
        assertThat(values.componentKey()).isEqualTo("/camera/manage");
        assertThat(values.moduleKey()).isEqualTo("camera");
    }

    @Test
    void normalizesFilePathAndPreservesExplicitModuleAndLegacyComponent() {
        var input = page();
        input.setRouteName("camera:Manage_1-edit");
        input.setPath("/camera/Manage_1-edit");
        input.setComponentKey("system/Department");
        input.setModuleKey("department");
        when(modules.contains("department")).thenReturn(true);
        assertThat(rules.write(input).componentKey()).isEqualTo("/system/Department");
        input.setComponentKey("SYSTEM_DEPARTMENTS");
        assertThat(rules.write(input).componentKey()).isEqualTo("SYSTEM_DEPARTMENTS");
    }

    @ParameterizedTest
    @ValueSource(
            strings = {
                "https://host/page",
                "//host/page",
                "/camera/../manage",
                "/camera/./manage",
                "/camera/%2e%2e/manage",
                "/camera/manage?mode=1",
                "/camera/manage#hash",
                "/camera\\manage",
                "/camera/manage.vue",
                "/camera//manage",
                "/camera/manage/"
            })
    void rejectsPathsThatCouldEscapeTheLocalPageRegistry(String path) {
        var input = page();
        input.setComponentKey(path);
        when(modules.contains("camera")).thenReturn(true);
        invalid(input);
        input.setComponentKey(null);
        input.setPath(path);
        invalid(input);
    }

    @ParameterizedTest
    @ValueSource(
            strings = {
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
                "/node_modules",
                "/Home/hidden",
                "/api/private",
                "/AUTH/me",
                "/Src/components",
                "/PUBLIC/logo",
                "/NODE_MODULES/vue"
            })
    void rejectsFixedAndInfrastructureRouteOverrides(String path) {
        var input = page();
        input.setPath(path);
        when(modules.contains("camera")).thenReturn(true);
        invalid(input);
    }

    @Test
    void rejectsUnknownModulesAndOverlongComponentPaths() {
        var input = page();
        invalid(input);
        when(modules.contains("camera")).thenReturn(true);
        input.setComponentKey("/camera/" + "a".repeat(57));
        invalid(input);
        input.setComponentKey(null);
        input.setRouteName("1camera");
        invalid(input);
    }

    private void invalid(MenuWriteDto input) {
        assertThatThrownBy(() -> rules.write(input))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> assertThat(error.code()).isEqualTo(ErrorCode.VALIDATION_ERROR));
    }

    private static MenuWriteDto page() {
        var input = new MenuWriteDto();
        input.setName("相机管理");
        input.setType("PAGE");
        input.setRouteName("camera");
        input.setPath("/camera/manage");
        input.setSortOrder(0);
        input.setVisible(true);
        input.setEnabled(true);
        return input;
    }
}
