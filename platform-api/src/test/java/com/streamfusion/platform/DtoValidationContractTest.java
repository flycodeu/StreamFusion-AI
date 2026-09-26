package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.catchThrowableOfType;
import static org.mockito.Mockito.mock;

import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.auth.pojo.dto.LoginDto;
import com.streamfusion.platform.auth.pojo.dto.PasswordChangeDto;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import com.streamfusion.platform.common.security.ModuleRegistry;
import com.streamfusion.platform.department.pojo.dto.DepartmentWriteDto;
import com.streamfusion.platform.department.service.DepartmentRules;
import com.streamfusion.platform.menu.pojo.dto.MenuWriteDto;
import com.streamfusion.platform.menu.service.MenuRules;
import com.streamfusion.platform.user.pojo.dto.UserCreateDto;
import com.streamfusion.platform.user.pojo.dto.UserProfileUpdateDto;
import com.streamfusion.platform.user.pojo.dto.UserQueryDto;
import com.streamfusion.platform.user.pojo.dto.UserVersionDto;
import com.streamfusion.platform.user.service.UserRules;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Duration;
import org.junit.jupiter.api.Test;
import org.springframework.security.crypto.password.PasswordEncoder;

/** 对照请求文档检查已有输入边界及安全的字段错误，不启动应用或访问数据库。 */
class DtoValidationContractTest {
    private final UserRules users = new UserRules();

    @Test
    void requiredAccountAndVersionMetadataMatchesExistingRules() throws Exception {
        for (Class<?> type : new Class<?>[] {UserCreateDto.class, LoginDto.class}) {
            Schema username = schema(type, "username");
            required(username);
            assertThat(username.minLength()).isEqualTo(4);
            assertThat(username.maxLength()).isEqualTo(32);
            assertThat("User01").matches(username.pattern());
        }
        assertThat(users.username("Ab01")).isEqualTo("Ab01");
        assertThat(users.username("A".repeat(32))).hasSize(32);
        invalidField(() -> users.username("Ab1"), "username");
        invalidField(() -> users.username("A".repeat(33)), "username");
        invalidField(() -> users.username(" User01 "), "username");
        invalidField(() -> users.username(null), "username");

        for (Class<?> type : new Class<?>[] {UserProfileUpdateDto.class, UserVersionDto.class}) {
            Schema version = schema(type, "version");
            required(version);
            assertThat(version.minLength()).isEqualTo(1);
            assertThat(version.maxLength()).isEqualTo(19);
            assertThat("0").matches(version.pattern());
        }
        assertThat(users.version("0")).isZero();
        assertThat(users.version("00")).isZero();
        assertThat(users.version(Long.toString(Long.MAX_VALUE))).isEqualTo(Long.MAX_VALUE);
        invalidField(() -> users.version(null), "version");
        invalidField(() -> users.version("-1"), "version");
        invalidField(() -> users.version("9223372036854775808"), "version");
    }

    @Test
    void invalidParentIdsReportTheRequestField() {
        DepartmentWriteDto department = new DepartmentWriteDto();
        department.setParentId("bad");
        department.setName("研发部");
        department.setSortOrder(0);
        invalidField(() -> new DepartmentRules().write(department), "parentId");

        MenuWriteDto menu = new MenuWriteDto();
        menu.setParentId("bad");
        menu.setName("系统管理");
        menu.setType("DIRECTORY");
        menu.setSortOrder(0);
        menu.setVisible(true);
        menu.setEnabled(true);
        invalidField(() -> new MenuRules(mock(ModuleRegistry.class)).write(menu), "parentId");
    }

    @Test
    void optionalProfileMetadataPreservesNormalizationAndClearing() throws Exception {
        for (Class<?> type : new Class<?>[] {UserCreateDto.class, UserProfileUpdateDto.class}) {
            for (String field :
                    new String[] {"nickname", "avatarKey", "phone", "email", "gender"}) {
                Schema optional = schema(type, field);
                assertThat(optional.requiredMode()).isEqualTo(Schema.RequiredMode.NOT_REQUIRED);
                assertThat(optional.nullable()).isTrue();
            }
            assertThat(schema(type, "gender").allowableValues()).containsExactly("0", "1", "2");
            assertThat(schema(type, "nickname").description()).isEqualTo("昵称");
        }
        assertThat(users.nickname("  " + "名".repeat(64) + "  ")).isEqualTo("名".repeat(64));
        assertThat(users.nickname("  ")).isNull();
        assertThat(users.avatarKey(null)).isNull();
        assertThat(users.avatarKey(" avatar-06 ")).isEqualTo("avatar-06");
        assertThat(users.phone(" +123456789 ")).isEqualTo("+123456789");
        assertThat(users.email(" person@example.com ")).isEqualTo("person@example.com");
        assertThat(users.gender(null)).isZero();
        invalidField(() -> users.nickname("名".repeat(65)), "nickname");
        invalidField(() -> users.avatarKey("avatar-07"), "avatarKey");
        invalidField(() -> users.phone("123456"), "phone");
        invalidField(() -> users.email("person..name@example.com"), "email");
        invalidField(() -> users.gender(3), "gender");
    }

    @Test
    void paginationReportsTheInvalidFieldAndPreservesBounds() throws Exception {
        PageQueryDto defaults = new PageQueryDto();
        assertThat(defaults.toPage().getCurrent()).isEqualTo(1);
        assertThat(defaults.toPage().getSize()).isEqualTo(20);
        assertThat(new PageQueryDto(1, PageQueryDto.MAX_SIZE).toPage().getSize())
                .isEqualTo(PageQueryDto.MAX_SIZE);
        assertThat(Integer.parseInt(schema(PageQueryDto.class, "size").maximum()))
                .isEqualTo(PageQueryDto.MAX_SIZE);
        invalidField(() -> new PageQueryDto(0, 20).toPage(), "page");
        invalidField(() -> new PageQueryDto(-1, 20).toPage(), "page");
        invalidField(() -> new PageQueryDto(1, 0).toPage(), "size");
        invalidField(() -> new PageQueryDto(1, 101).toPage(), "size");
    }

    @Test
    void userFiltersReportFieldsAndCountUnicodeCodePoints() throws Exception {
        new UserQueryDto("😀".repeat(64), null).validateFilters();
        for (int status = 0; status <= 2; status++) {
            new UserQueryDto(null, status).validateFilters();
        }
        invalidField(() -> new UserQueryDto("😀".repeat(65), null).validateFilters(), "keyword");
        invalidField(() -> new UserQueryDto(null, -1).validateFilters(), "status");
        invalidField(() -> new UserQueryDto(null, 3).validateFilters(), "status");
        assertThat(schema(UserQueryDto.class, "keyword").maxLength()).isEqualTo(64);
        assertThat(schema(UserQueryDto.class, "status").allowableValues())
                .containsExactly("0", "1", "2");
        assertThat(schema(UserQueryDto.class, "status").nullable()).isTrue();
    }

    @Test
    void passwordRulesUseConfigurationAndExposeOnlySafeFieldDetails() throws Exception {
        PasswordService passwords = passwords("Initial9!Xab");
        passwords.validateNewPassword("Valid9!Xab");
        invalidField(() -> passwords.validateNewPassword(null), "newPassword");
        invalidField(() -> passwords.validateNewPassword("Ab1!abcd"), "newPassword");
        invalidField(() -> passwords.validateNewPassword("Ab1!" + "a".repeat(9)), "newPassword");
        invalidField(() -> passwords.validateNewPassword("Valid9 Xab"), "newPassword");
        invalidField(() -> passwords.validateNewPassword("valid9!xab"), "newPassword");
        invalidField(
                () -> passwords.validateReplacement("Valid9!Xab", "Valid9!Xab"), "newPassword");
        invalidField(
                () -> passwords.validateReplacement("Other9!Xab", "Initial9!Xab"), "newPassword");
        for (String field : new String[] {"currentPassword", "newPassword"}) {
            Schema password = schema(PasswordChangeDto.class, field);
            required(password);
            assertThat(password.accessMode()).isEqualTo(Schema.AccessMode.WRITE_ONLY);
            assertThat(password.minLength()).isZero();
            assertThat(password.maxLength()).isEqualTo(Integer.MAX_VALUE);
        }
        assertThat(schema(PasswordChangeDto.class, "newPassword").description()).isEqualTo("新密码");
        BusinessException unavailable =
                catchThrowableOfType(
                        () -> passwords("short").initialPasswordHash(), BusinessException.class);
        assertThat(unavailable.code()).isEqualTo(ErrorCode.INITIAL_PASSWORD_UNAVAILABLE);
        assertThat(unavailable.safeDetails()).isNull();
    }

    private static Schema schema(Class<?> type, String field) throws NoSuchFieldException {
        return type.getDeclaredField(field).getAnnotation(Schema.class);
    }

    private static void required(Schema schema) {
        assertThat(schema.requiredMode()).isEqualTo(Schema.RequiredMode.REQUIRED);
        assertThat(schema.nullable()).isFalse();
    }

    private static void invalidField(Runnable action, String field) {
        BusinessException failure = catchThrowableOfType(action::run, BusinessException.class);
        assertThat(failure).isNotNull();
        assertThat(failure.code()).isEqualTo(ErrorCode.VALIDATION_ERROR);
        assertThat(failure.safeDetails())
                .isEqualTo(
                        new ValidationDetails(
                                java.util.List.of(ValidationDetails.FieldError.invalid(field))));
    }

    private static PasswordService passwords(String initialPassword) {
        PasswordEncoder encoder =
                new PasswordEncoder() {
                    @Override
                    public String encode(CharSequence rawPassword) {
                        return "synthetic-test-hash";
                    }

                    @Override
                    public boolean matches(CharSequence rawPassword, String encodedPassword) {
                        return false;
                    }
                };
        return new PasswordService(
                encoder,
                new AuthProperties(
                        10,
                        12,
                        true,
                        true,
                        true,
                        true,
                        600000,
                        Duration.ofMinutes(30),
                        Duration.ofHours(8),
                        true,
                        initialPassword,
                        0,
                        0));
    }
}
