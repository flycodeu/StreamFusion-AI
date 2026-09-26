package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verify;
import static org.mockito.Mockito.verifyNoInteractions;

import com.streamfusion.platform.auth.bootstrap.BootstrapRunner;
import com.streamfusion.platform.auth.controller.AuthController;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.service.UserService;
import java.util.Arrays;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.NullSource;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.DefaultApplicationArguments;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.ApplicationContext;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.env.MockEnvironment;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.test.context.ActiveProfiles;

@SpringBootTest(
        webEnvironment = SpringBootTest.WebEnvironment.NONE,
        properties = {
            "spring.datasource.url=jdbc:h2:mem:bootstrap_context;MODE=MySQL;DB_CLOSE_DELAY=-1"
        })
@ActiveProfiles("test")
class BootstrapContextTest {
    @Autowired ApplicationContext context;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired UserService users;
    @Autowired PasswordService passwords;
    @Autowired Environment environment;
    private JdbcTemplate jdbc;

    @BeforeEach
    void resetSchema() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        // Prove the bootstrap transaction fills missing page bindings itself.
        jdbc.update("DELETE FROM sys_role_menu");
    }

    @Test
    void bootstrapServicesRunWithoutWebSecurityOrNetworkEntry() {
        assertThat(context.getBeansOfType(AuthController.class)).isEmpty();
        assertThat(context.getBeansOfType(SecurityFilterChain.class)).isEmpty();
        long id = bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        assertThat(id).isGreaterThan(9007199254740991L);
        var user = users.getById(id);
        assertThat(user.getStatus()).isEqualTo(UserStatus.NORMAL.getCode());
        assertThat(user.getMustChangePassword()).isFalse();
        assertThat(count("sys_role_menu")).isEqualTo(4);
        assertThat(count("sys_user_dept")).isZero();
    }

    @Test
    void defaultAdministratorIsPersistedWithPendingPasswordAndCannotBeResetByBootstrap() {
        String fixturePassword = "starter@2026";
        long id = bootstrap.initializeDefaultAdmin("admin", "超级管理员", fixturePassword, 2002L);
        var user = users.getById(id);
        assertThat(id).isGreaterThan(9007199254740991L);
        assertThat(user.getUsername()).isEqualTo("admin");
        assertThat(user.getNickname()).isEqualTo("超级管理员");
        assertThat(user.getStatus()).isEqualTo(UserStatus.PENDING_PASSWORD.getCode());
        assertThat(user.getMustChangePassword()).isTrue();
        assertThat(user.getPassword())
                .startsWith("{pbkdf2-sha256-v1}")
                .isNotEqualTo(fixturePassword);
        assertThat(passwords.matches(fixturePassword, user.getPassword())).isTrue();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user_role ur"
                                        + " JOIN sys_role r ON r.id=ur.role_id"
                                        + " WHERE ur.user_id=? AND r.code='SUPER_ADMIN'",
                                Integer.class,
                                id))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu rm"
                                        + " JOIN sys_user_role ur ON ur.role_id=rm.role_id"
                                        + " WHERE ur.user_id=?",
                                Integer.class,
                                id))
                .isEqualTo(4);

        assertThat(
                        jdbc.queryForObject(
                                "SELECT dept_id FROM sys_user_dept WHERE user_id=?",
                                Long.class,
                                id))
                .isEqualTo(2002L);
        assertThatThrownBy(
                        () ->
                                bootstrap.initializeDefaultAdmin(
                                        "admin", "新名称", "another@2026", 2003L))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        ex -> assertThat(ex.code()).isEqualTo(ErrorCode.BOOTSTRAP_UNAVAILABLE));
        assertThat(users.getById(id)).isEqualTo(user);
        assertThat(count("sys_user")).isEqualTo(1);
        assertThat(count("sys_user_role")).isEqualTo(1);
        assertThat(count("sys_role_menu")).isEqualTo(4);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT dept_id FROM sys_user_dept WHERE user_id=?",
                                Long.class,
                                id))
                .isEqualTo(2002L);
        assertThat(count("sys_operation_log")).isEqualTo(1);
    }

    @Test
    void defaultCredentialValidationDoesNotRelaxTheManualPasswordRule() {
        for (String invalid :
                Arrays.asList(null, "short", "x".repeat(65), "initial pass", "initial\npass")) {
            assertThatThrownBy(() -> bootstrap.initializeDefaultAdmin("admin", null, invalid, null))
                    .isInstanceOfSatisfying(
                            BusinessException.class,
                            ex -> assertThat(ex.code()).isEqualTo(ErrorCode.VALIDATION_ERROR));
        }
        assertThatThrownBy(() -> bootstrap.initialize("Admin01", null, "starter@2026", null))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        ex -> assertThat(ex.code()).isEqualTo(ErrorCode.VALIDATION_ERROR));
        assertThat(count("sys_user")).isZero();
    }

    @Test
    void defaultBootstrapPreservesAlreadySeededPageBindings() throws Exception {
        IdentitySchema.initialize(source);
        assertThat(count("sys_role_menu")).isEqualTo(4);
        bootstrap.initializeDefaultAdmin("admin", null, "starter@2026", null);
        assertThat(count("sys_user")).isEqualTo(1);
        assertThat(count("sys_user_role")).isEqualTo(1);
        assertThat(count("sys_role_menu")).isEqualTo(4);
    }

    @Test
    void auditFailureRollsBackDefaultUserAndBothBindings() {
        jdbc.execute(
                "ALTER TABLE sys_operation_log ADD CONSTRAINT reject_default_bootstrap"
                        + " CHECK (action <> 'BOOTSTRAP')");
        assertThatThrownBy(
                        () ->
                                bootstrap.initializeDefaultAdmin(
                                        "admin", null, "starter@2026", 2002L))
                .isInstanceOf(RuntimeException.class);
        assertThat(count("sys_user")).isZero();
        assertThat(count("sys_user_role")).isZero();
        assertThat(count("sys_role_menu")).isZero();
        assertThat(count("sys_user_dept")).isZero();
        assertThat(count("sys_operation_log")).isZero();
    }

    @Test
    void defaultRunnerRequiresAnExplicitInitialCredential() {
        BootstrapService service = mock(BootstrapService.class);
        var environment =
                new MockEnvironment().withProperty("platform.bootstrap.default-admin", "true");
        var runner = new BootstrapRunner(service, environment);
        assertThatThrownBy(() -> runner.run(new DefaultApplicationArguments()))
                .isInstanceOf(IllegalStateException.class)
                .hasMessageContaining("SF_BOOTSTRAP_PASSWORD");
        verifyNoInteractions(service);
    }

    @Test
    void defaultRunnerPassesConfiguredAccountAndDepartment() {
        BootstrapService service = mock(BootstrapService.class);
        var environment =
                new MockEnvironment()
                        .withProperty("platform.bootstrap.default-admin", "true")
                        .withProperty("platform.bootstrap.username", "Owner01")
                        .withProperty("platform.bootstrap.nickname", "部署管理员")
                        .withProperty("platform.bootstrap.department-id", "2003")
                        .withProperty("SF_BOOTSTRAP_PASSWORD", "starter@2026");
        new BootstrapRunner(service, environment).run(new DefaultApplicationArguments());
        verify(service).initializeDefaultAdmin("Owner01", "部署管理员", "starter@2026", 2003L);
    }

    @Test
    void applicationSuppliesEditableDefaultAccountAndDepartment() {
        assertThat(environment.getProperty("platform.bootstrap.username")).isEqualTo("admin");
        assertThat(environment.getProperty("platform.bootstrap.nickname")).isEqualTo("超级管理员");
        assertThat(environment.getProperty("platform.bootstrap.department-id", Long.class))
                .isEqualTo(2002L);
    }

    @Test
    void defaultRunnerAcceptsAnExplicitlyBlankDepartment() {
        BootstrapService service = mock(BootstrapService.class);
        var settings =
                new MockEnvironment()
                        .withProperty("platform.bootstrap.default-admin", "true")
                        .withProperty("platform.bootstrap.username", "Owner01")
                        .withProperty("platform.bootstrap.department-id", "")
                        .withProperty("SF_BOOTSTRAP_PASSWORD", "starter@2026");
        new BootstrapRunner(service, settings).run(new DefaultApplicationArguments());
        verify(service).initializeDefaultAdmin("Owner01", null, "starter@2026", null);
    }

    @ParameterizedTest
    @NullSource
    @ValueSource(longs = {2003L})
    void configuredBootstrapSupportsCustomAccountAndOptionalDepartment(Long departmentId) {
        long id =
                bootstrap.initializeDefaultAdmin("Owner01", "自定义管理员", "starter@2026", departmentId);
        assertThat(users.getById(id).getUsername()).isEqualTo("Owner01");
        assertThat(users.getById(id).getNickname()).isEqualTo("自定义管理员");
        assertThat(users.getById(id).getStatus()).isEqualTo(UserStatus.PENDING_PASSWORD.getCode());
        if (departmentId == null) assertThat(count("sys_user_dept")).isZero();
        else
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT dept_id FROM sys_user_dept WHERE user_id=?",
                                    Long.class,
                                    id))
                    .isEqualTo(departmentId);
    }

    @Test
    void interactiveBootstrapCanBindSelectedDepartmentWithNormalAccount() {
        long id = bootstrap.initialize("Owner01", "自定义管理员", "OwnerPass1!", 2003L);
        assertThat(users.getById(id).getStatus()).isEqualTo(UserStatus.NORMAL.getCode());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT dept_id FROM sys_user_dept WHERE user_id=?",
                                Long.class,
                                id))
                .isEqualTo(2003L);
    }

    @ParameterizedTest
    @ValueSource(longs = {-1L, 0L, 999999L})
    void invalidDepartmentLeavesBootstrapUnchanged(long departmentId) {
        assertThatThrownBy(
                        () ->
                                bootstrap.initializeDefaultAdmin(
                                        "admin", null, "starter@2026", departmentId))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        ex -> assertThat(ex.code()).isEqualTo(ErrorCode.VALIDATION_ERROR));
        assertThat(count("sys_user")).isZero();
        assertThat(count("sys_user_role")).isZero();
        assertThat(count("sys_role_menu")).isZero();
        assertThat(count("sys_user_dept")).isZero();
        assertThat(count("sys_operation_log")).isZero();
    }

    @Test
    void independentInitializationsHashTheSuppliedPasswordWithDifferentSalts() throws Exception {
        long firstId = bootstrap.initializeDefaultAdmin("admin", null, "starter@2026", 2002L);
        String firstHash = users.getById(firstId).getPassword();
        IdentitySchema.initialize(source);
        long secondId = bootstrap.initializeDefaultAdmin("admin", null, "starter@2026", 2002L);
        String secondHash = users.getById(secondId).getPassword();
        assertThat(secondHash).isNotEqualTo(firstHash);
        assertThat(passwords.matches("starter@2026", firstHash)).isTrue();
        assertThat(passwords.matches("starter@2026", secondHash)).isTrue();
    }

    private int count(String table) {
        return jdbc.queryForObject("SELECT COUNT(*) FROM " + table, Integer.class);
    }
}
