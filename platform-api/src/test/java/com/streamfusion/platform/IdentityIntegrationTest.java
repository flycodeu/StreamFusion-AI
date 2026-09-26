package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.AuthenticationService;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.user.service.UserService;
import java.time.Instant;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.Callable;
import java.util.concurrent.Executors;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.security.authentication.UsernamePasswordAuthenticationToken;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:identity;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class IdentityIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired DataSource source;
    @Autowired ObjectMapper json;
    @Autowired BootstrapService bootstrap;
    @Autowired AuthenticationService authentication;
    @Autowired PasswordService passwords;
    @Autowired UserService users;
    private JdbcTemplate jdbc;

    @BeforeEach
    void resetSchema() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        SecurityContextHolder.clearContext();
    }

    @Test
    void bootstrapsExactlyOnceWithSnowflakeAndRealRoleBinding() {
        long id = bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        assertThat(id).isGreaterThan(9007199254740991L);
        assertThat(users.getById(id).getNickname()).isNull();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user_role WHERE user_id=?",
                                Integer.class,
                                id))
                .isEqualTo(1);
        assertThatThrownBy(() -> bootstrap.initialize("Admin02", null, "AdminPass1!", null))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        ex -> assertThat(ex.code()).isEqualTo(ErrorCode.BOOTSTRAP_UNAVAILABLE));
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_user", Integer.class))
                .isEqualTo(1);
    }

    @Test
    void bootstrapCompetitionCreatesOneCompleteIdentity() throws Exception {
        try (var executor = Executors.newFixedThreadPool(2)) {
            Callable<Boolean> attempt =
                    () -> {
                        try {
                            bootstrap.initialize("Admin01", null, "AdminPass1!", null);
                            return true;
                        } catch (BusinessException ex) {
                            return false;
                        }
                    };
            var outcomes = executor.invokeAll(List.of(attempt, attempt));
            assertThat(
                            outcomes.stream()
                                    .filter(
                                            f -> {
                                                try {
                                                    return f.get();
                                                } catch (Exception ex) {
                                                    throw new RuntimeException(ex);
                                                }
                                            })
                                    .count())
                    .isEqualTo(1);
        }
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_user", Integer.class))
                .isEqualTo(1);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_user_role", Integer.class))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='BOOTSTRAP'",
                                Integer.class))
                .isEqualTo(1);
    }

    @Test
    void loginRotatesSessionAndReturnsOnlySafeIdentity() throws Exception {
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        Csrf csrf = csrf(null);
        String oldId = csrf.session().getId();
        MvcResult result =
                mvc.perform(
                                post("/auth/login")
                                        .session(csrf.session())
                                        .header(csrf.header(), csrf.token())
                                        .contentType("application/json")
                                        .content(
                                                json.writeValueAsString(
                                                        Map.of(
                                                                "username",
                                                                "Admin01",
                                                                "password",
                                                                "AdminPass1!"))))
                        .andExpect(status().isOk())
                        .andExpect(jsonPath("$.data").isEmpty())
                        .andReturn();
        var session = (MockHttpSession) result.getRequest().getSession(false);
        assertThat(session.getId()).isNotEqualTo(oldId);
        mvc.perform(get("/auth/me").session(session))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.isSuperAdmin").value(true))
                .andExpect(jsonPath("$.data.permissions").doesNotExist())
                .andExpect(jsonPath("$.data.modules.length()").value(7))
                .andExpect(jsonPath("$.data.user.status").value(1))
                .andExpect(jsonPath("$.data.user.id").isString())
                .andExpect(jsonPath("$.data.user.version").isString())
                .andExpect(jsonPath("$.data.user.password").doesNotExist())
                .andExpect(jsonPath("$.data.user.sessionVersion").doesNotExist());
        mvc.perform(
                        put("/auth/me")
                                .session(session)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\"}"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("CSRF_INVALID"));
    }

    @Test
    void firstLoginIsRestrictedUntilPasswordChangeAndAllOldSessionsExpire() throws Exception {
        var admin = admin();
        String id = create(admin, "Worker01");
        var first = login("Worker01", "Initial1!");
        var second = login("Worker01", "Initial1!");
        mvc.perform(get("/auth/me").session(first))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.user.status").value(0))
                .andExpect(jsonPath("$.data.isSuperAdmin").value(false));
        for (String path : List.of("/auth/csrf", "/auth/me", "/auth/password-policy")) {
            mvc.perform(head(path).session(first)).andExpect(status().isOk());
        }
        mvc.perform(head("/user/page").session(first))
                .andExpect(status().isForbidden())
                .andExpect(content().string(""));
        Csrf csrf = csrf(first);
        mvc.perform(
                        put("/auth/me")
                                .session(first)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"nickname\":\"张三\",\"version\":\"0\"}"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PASSWORD_CHANGE_REQUIRED"));
        mvc.perform(
                        put("/auth/password")
                                .session(first)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content(
                                        "{\"currentPassword\":\"Initial1!\",\"newPassword\":\"Personal2@\"}"))
                .andExpect(status().isOk());
        mvc.perform(get("/auth/me").session(second)).andExpect(status().isUnauthorized());
        assertThat(users.getById(Long.parseLong(id)).getStatus()).isEqualTo(1);
        var normal = login("Worker01", "Personal2@");
        Csrf normalCsrf = csrf(normal);
        mvc.perform(
                        put("/auth/me")
                                .session(normal)
                                .header(normalCsrf.header(), normalCsrf.token())
                                .contentType("application/json")
                                .content(
                                        "{\"nickname\":\"张三\",\"phone\":\"13800138000\",\"email\":\"a@example.com\",\"gender\":1,\"version\":\"1\"}"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.nickname").value("张三"));
        mvc.perform(
                        put("/auth/me")
                                .session(normal)
                                .header(normalCsrf.header(), normalCsrf.token())
                                .contentType("application/json")
                                .content(
                                        "{\"nickname\":null,\"phone\":null,\"email\":null,\"version\":\"2\"}"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.nickname").isEmpty());
        mvc.perform(
                        put("/auth/me")
                                .session(normal)
                                .header(normalCsrf.header(), normalCsrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"3\",\"roleIds\":[\"1\"]}"))
                .andExpect(status().isBadRequest());
        mvc.perform(get("/user/page").session(normal)).andExpect(status().isForbidden());
    }

    @Test
    void fifthFailureLocksAndExpiredCooldownStartsNewCycle() throws Exception {
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        for (int i = 0; i < 5; i++) {
            assertThatThrownBy(() -> authentication.authenticate("Admin01", "WrongPass1!", null))
                    .isInstanceOfSatisfying(
                            BusinessException.class,
                            ex -> assertThat(ex.code()).isEqualTo(ErrorCode.LOGIN_FAILED));
        }
        var user = users.findByUsername("Admin01");
        assertThat(user.getFailedLoginCount()).isEqualTo(5);
        assertThat(user.getLockedUntil()).isNotNull();
        assertThatThrownBy(() -> authentication.authenticate("Admin01", "AdminPass1!", null))
                .isInstanceOf(BusinessException.class);
        assertThat(users.getById(user.getId()).getLockedUntil()).isEqualTo(user.getLockedUntil());
        jdbc.update(
                "UPDATE sys_user SET locked_until=DATEADD('MINUTE',-1,CURRENT_TIMESTAMP) WHERE id=?",
                user.getId());
        assertThat(authentication.authenticate("Admin01", "AdminPass1!", null).getUserId())
                .isEqualTo(user.getId());
        assertThat(users.getById(user.getId()).getFailedLoginCount()).isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='LOGIN' AND result='FAILURE'",
                                Integer.class))
                .isEqualTo(5);
    }

    @Test
    void adminCannotMutateSuperAdminAndUsersCannotElevateThroughUnknownFields() throws Exception {
        var admin = admin();
        String id = users.findByUsername("Admin01").getId().toString();
        Csrf csrf = csrf(admin);
        mvc.perform(
                        post("/user/" + id + "/reset-password")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\"}"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PROTECTED_ACCOUNT"));
        mvc.perform(
                        post("/user")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"username\":\"Worker01\",\"roleIds\":[\"1\"]}"))
                .andExpect(status().isBadRequest());
        assertThat(users.count()).isEqualTo(1);
    }

    @Test
    void banResetUnbanPreservesRestrictionAndRejectsPreviousSessions() throws Exception {
        var admin = admin();
        String id = create(admin, "Worker01");
        var old = login("Worker01", "Initial1!");
        adminAction(admin, id, "/disable", "0", 2);
        mvc.perform(get("/auth/me").session(old)).andExpect(status().isUnauthorized());
        Csrf csrf = csrf(admin);
        mvc.perform(
                        post("/user/" + id + "/reset-password")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"1\"}"))
                .andExpect(status().isOk());
        assertThat(users.getById(Long.parseLong(id)).getStatus()).isEqualTo(2);
        adminAction(admin, id, "/enable", "2", 0);
        assertThat(login("Worker01", "Initial1!")).isNotNull();
    }

    @Test
    void temporaryPasswordIsReturnedOnlyBySuccessfulCreateAndReset() throws Exception {
        var admin = admin();
        Csrf csrf = csrf(admin);
        var created =
                mvc.perform(
                                post("/user")
                                        .session(admin)
                                        .header(csrf.header(), csrf.token())
                                        .contentType("application/json")
                                        .content("{\"username\":\"Worker01\"}"))
                        .andExpect(status().isCreated())
                        .andExpect(header().string("Cache-Control", "no-store"))
                        .andExpect(jsonPath("$.data.temporaryPassword").value("Initial1!"))
                        .andReturn();
        String id =
                json.readTree(created.getResponse().getContentAsString())
                        .path("data")
                        .path("id")
                        .asText();
        var previousSession = login("Worker01", "Initial1!");
        mvc.perform(
                        post("/user/" + id + "/reset-password")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\"}"))
                .andExpect(status().isOk())
                .andExpect(header().string("Cache-Control", "no-store"))
                .andExpect(header().string("ETag", "\"1\""))
                .andExpect(jsonPath("$.data.temporaryPassword").value("Initial1!"))
                .andExpect(jsonPath("$.data.version").value("1"))
                .andExpect(jsonPath("$.data.mustChangePassword").value(true));
        mvc.perform(get("/auth/me").session(previousSession)).andExpect(status().isUnauthorized());
        mvc.perform(get("/user/" + id).session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.temporaryPassword").doesNotExist());
        mvc.perform(get("/user/page").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.items[*].temporaryPassword").doesNotExist());
        mvc.perform(
                        post("/user/" + id + "/reset-password")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\"}"))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.data.temporaryPassword").doesNotExist());
        assertThat(
                        jdbc.queryForList(
                                "SELECT changes FROM sys_operation_log WHERE target_type='USER'",
                                String.class))
                .allSatisfy(
                        value -> {
                            if (value != null)
                                assertThat(value).doesNotContain("Initial1!", "temporaryPassword");
                        });
    }

    @Test
    void userAndOptionalDepartmentsSaveAtomicallyWithOneEditVersion() throws Exception {
        var admin = admin();
        Csrf csrf = csrf(admin);
        var created =
                mvc.perform(
                                post("/user")
                                        .session(admin)
                                        .header(csrf.header(), csrf.token())
                                        .contentType("application/json")
                                        .content(
                                                "{\"username\":\"Worker01\",\"nickname\":\"首次\",\"departmentIds\":[\"2002\",\"2003\"]}"))
                        .andExpect(status().isCreated())
                        .andExpect(jsonPath("$.data.version").value("0"))
                        .andExpect(jsonPath("$.data.departments.length()").value(2))
                        .andReturn();
        String id =
                json.readTree(created.getResponse().getContentAsString())
                        .path("data")
                        .path("id")
                        .asText();
        mvc.perform(
                        put("/user/" + id)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content(
                                        "{\"nickname\":\"已编辑\",\"version\":\"0\",\"departmentIds\":[\"2003\"]}"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.version").value("1"))
                .andExpect(jsonPath("$.data.departments.length()").value(1));
        mvc.perform(
                        put("/user/" + id)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"nickname\":\"保留部门\",\"version\":\"1\"}"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.version").value("2"))
                .andExpect(jsonPath("$.data.departments[0].id").value("2003"));
        mvc.perform(
                        put("/user/" + id)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content(
                                        "{\"nickname\":\"应回滚\",\"version\":\"2\",\"departmentIds\":[\"999999\"]}"))
                .andExpect(status().isBadRequest());
        mvc.perform(get("/user/" + id).session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.nickname").value("保留部门"))
                .andExpect(jsonPath("$.data.version").value("2"))
                .andExpect(jsonPath("$.data.departments[0].id").value("2003"));
        mvc.perform(
                        put("/user/" + id)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content(
                                        "{\"nickname\":\"已清空\",\"version\":\"2\",\"departmentIds\":[]}"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.version").value("3"))
                .andExpect(jsonPath("$.data.departments.length()").value(0));
        mvc.perform(
                        post("/user")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content(
                                        "{\"username\":\"InvalidUser\",\"departmentIds\":[\"999999\"]}"))
                .andExpect(status().isBadRequest());
        assertThat(users.findByUsername("InvalidUser")).isNull();
    }

    @Test
    void deletionReleasesAccountWithoutReusingIdentityOrBindings() throws Exception {
        var admin = admin();
        String firstId = create(admin, "Worker01");
        jdbc.update("UPDATE sys_user SET nickname='原账号名称' WHERE id=?", Long.parseLong(firstId));
        var old = login("Worker01", "Initial1!");
        Csrf csrf = csrf(admin);
        mvc.perform(delete("/user/" + firstId).session(admin).header(csrf.header(), csrf.token()))
                .andExpect(status().isPreconditionRequired());
        mvc.perform(
                        delete("/user/" + firstId)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        mvc.perform(get("/auth/me").session(old)).andExpect(status().isUnauthorized());
        String nextId = create(admin, "worker01");
        assertThat(nextId).isNotEqualTo(firstId);
        String summary =
                jdbc.queryForObject(
                        "SELECT changes FROM sys_operation_log WHERE action='USER_DELETE' AND target_id=?",
                        String.class,
                        Long.parseLong(firstId));
        JsonNode changes = json.readTree(summary);
        assertThat(changes.path("username").asText()).isEqualTo("Worker01");
        assertThat(changes.path("nickname").asText()).isEqualTo("原账号名称");
        assertThat(summary).doesNotContain("password", "Initial1!", "pbkdf2");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user WHERE LOWER(username)='worker01'",
                                Integer.class))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user_role WHERE user_id=?",
                                Integer.class,
                                Long.parseLong(nextId)))
                .isZero();
        mvc.perform(get("/user/page").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.total").value(2));
    }

    @Test
    void profileVersionAndAbsoluteSessionLifetimeAreEnforced() throws Exception {
        var admin = admin();
        Csrf csrf = csrf(admin);
        mvc.perform(
                        put("/auth/me")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"99\"}"))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("VERSION_CONFLICT"));
        var user = users.findByUsername("Admin01");
        var context = SecurityContextHolder.createEmptyContext();
        context.setAuthentication(
                UsernamePasswordAuthenticationToken.authenticated(
                        new SessionPrincipalDto(
                                user.getId(), 0L, Instant.now().minusSeconds(28801)),
                        null,
                        List.of()));
        admin.setAttribute(
                HttpSessionSecurityContextRepository.SPRING_SECURITY_CONTEXT_KEY, context);
        mvc.perform(get("/auth/me").session(admin)).andExpect(status().isUnauthorized());
    }

    @Test
    void sharedPaginationUsesSqlLimitsCountsFiltersAndStableEnvelope() throws Exception {
        var admin = admin();
        String first = create(admin, "Worker01");
        String second = create(admin, "Worker02");
        String deleted = create(admin, "Worker03");
        jdbc.update("DELETE FROM sys_user WHERE id=?", Long.parseLong(deleted));
        jdbc.update("UPDATE sys_user SET nickname='team_100%!' WHERE id=?", Long.parseLong(first));
        jdbc.update(
                "UPDATE sys_user SET nickname='teamA100other' WHERE id=?", Long.parseLong(second));

        mvc.perform(get("/user/page").session(admin))
                .andExpect(status().isOk())
                .andExpect(header().string("Cache-Control", "no-store"))
                .andExpect(jsonPath("$.code").value("SUCCESS"))
                .andExpect(jsonPath("$.data.page").value(1))
                .andExpect(jsonPath("$.data.size").value(20))
                .andExpect(jsonPath("$.data.total").value(3))
                .andExpect(jsonPath("$.data.items.length()").value(3))
                .andExpect(jsonPath("$.data.records").doesNotExist())
                .andExpect(jsonPath("$.data.items[0].password").doesNotExist());
        mvc.perform(get("/user/page").session(admin).param("page", "2").param("size", "1"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.page").value(2))
                .andExpect(jsonPath("$.data.size").value(1))
                .andExpect(jsonPath("$.data.total").value(3))
                .andExpect(jsonPath("$.data.items.length()").value(1))
                .andExpect(jsonPath("$.data.items[0].id").value(first));
        mvc.perform(get("/user/page").session(admin).param("page", "4").param("size", "1"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.page").value(4))
                .andExpect(jsonPath("$.data.total").value(3))
                .andExpect(jsonPath("$.data.items").isEmpty());
        mvc.perform(
                        get("/user/page")
                                .session(admin)
                                .param("keyword", "wOrKeR")
                                .param("status", "0"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.total").value(2));
        for (String literal : List.of("100%", "_", "!")) {
            mvc.perform(get("/user/page").session(admin).param("keyword", literal))
                    .andExpect(status().isOk())
                    .andExpect(jsonPath("$.data.total").value(1))
                    .andExpect(jsonPath("$.data.items[0].id").value(first));
        }
        mvc.perform(get("/user/page").session(admin).param("keyword", "' OR 1=1 --"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.total").value(0));
    }

    @Test
    void sharedPaginationRejectsInvalidParametersThroughUnifiedErrors() throws Exception {
        var admin = admin();
        for (String query :
                List.of(
                        "?page=0",
                        "?page=-1",
                        "?size=0",
                        "?size=-1",
                        "?size=101",
                        "?page=2147483648",
                        "?size=abc",
                        "?status=3")) {
            mvc.perform(get("/user/page" + query).session(admin))
                    .andExpect(status().isBadRequest())
                    .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"))
                    .andExpect(jsonPath("$.traceId").isString());
        }
        mvc.perform(get("/user/page").session(admin).param("keyword", "x".repeat(65)))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
        mvc.perform(get("/user/page").session(admin).param("size", "100"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.size").value(100));
        mvc.perform(get("/api/v1/users").session(admin)).andExpect(status().isForbidden());
        mvc.perform(get("/userland/page").session(admin)).andExpect(status().isForbidden());
    }

    @Test
    void responseModelsExposeOnlyFieldsRequiredByEachUseCase() throws Exception {
        var admin = admin();
        String id = create(admin, "Worker01");
        var detail =
                mvc.perform(get("/user/" + id).session(admin))
                        .andExpect(status().isOk())
                        .andReturn();
        JsonNode user = json.readTree(detail.getResponse().getContentAsString()).path("data");
        assertThat(fieldNames(user))
                .containsExactlyInAnyOrder(
                        "id",
                        "username",
                        "nickname",
                        "avatarKey",
                        "phone",
                        "email",
                        "gender",
                        "status",
                        "mustChangePassword",
                        "departments",
                        "roles",
                        "version",
                        "lockedUntil",
                        "loginRestricted");

        var listing =
                mvc.perform(get("/user/page").session(admin))
                        .andExpect(status().isOk())
                        .andReturn();
        JsonNode summary =
                json.readTree(listing.getResponse().getContentAsString())
                        .path("data")
                        .path("items")
                        .get(0);
        assertThat(fieldNames(summary))
                .containsExactlyInAnyOrder(
                        "id",
                        "username",
                        "nickname",
                        "avatarKey",
                        "status",
                        "departments",
                        "roles",
                        "version",
                        "lockedUntil",
                        "loginRestricted");

        var me = mvc.perform(get("/auth/me").session(admin)).andExpect(status().isOk()).andReturn();
        JsonNode identity = json.readTree(me.getResponse().getContentAsString()).path("data");
        assertThat(fieldNames(identity))
                .containsExactlyInAnyOrder("user", "modules", "roles", "routes", "isSuperAdmin");
        assertThat(fieldNames(identity.path("user")))
                .containsExactlyInAnyOrder(
                        "id",
                        "username",
                        "nickname",
                        "avatarKey",
                        "phone",
                        "email",
                        "gender",
                        "status",
                        "mustChangePassword",
                        "version");
    }

    @Test
    void openApiDocumentsActualDtosVosAndWriteOnlyCredentials() throws Exception {
        var result = mvc.perform(get("/v3/api-docs")).andExpect(status().isOk()).andReturn();
        JsonNode document = json.readTree(result.getResponse().getContentAsString());
        JsonNode schemas = document.path("components").path("schemas");
        assertThat(
                        schemas.path("LoginDto")
                                .path("properties")
                                .path("password")
                                .path("writeOnly")
                                .asBoolean())
                .isTrue();
        assertThat(
                        schemas.path("PasswordChangeDto")
                                .path("properties")
                                .path("newPassword")
                                .path("writeOnly")
                                .asBoolean())
                .isTrue();
        assertThat(
                        schemas.path("UserVo")
                                .path("properties")
                                .path("username")
                                .path("description")
                                .asText())
                .isNotBlank();
        assertThat(schemas.path("UserProfileVo").path("properties").has("roles")).isFalse();
        assertThat(schemas.path("UserSummaryVo").path("properties").has("phone")).isFalse();
        assertThat(schemas.has("UserEntity")).isFalse();
        assertThat(schemas.has("UserView")).isFalse();
        JsonNode parameters =
                document.path("paths").path("/user/page").path("get").path("parameters");
        Set<String> names = new HashSet<>();
        parameters.forEach(
                parameter -> {
                    names.add(parameter.path("name").asText());
                    assertThat(parameter.path("description").asText()).isNotBlank();
                });
        assertThat(names).contains("page", "size", "keyword", "status");
    }

    private static Set<String> fieldNames(JsonNode node) {
        Set<String> names = new HashSet<>();
        node.fieldNames().forEachRemaining(names::add);
        return names;
    }

    @Test
    void passwordHashesUseIndependentSaltsAndRulesAreCentralized() {
        String first = passwords.encode("SamePass1!");
        String second = passwords.encode("SamePass1!");
        assertThat(first).isNotEqualTo(second).startsWith("{pbkdf2-sha256-v1}");
        assertThat(passwords.matches("SamePass1!", first)).isTrue();
        assertThatThrownBy(() -> passwords.validateNewPassword("lowercase1!"))
                .isInstanceOf(BusinessException.class);
        assertThatCode(() -> passwords.validateNewPassword("中文Strong1!"))
                .doesNotThrowAnyException();
    }

    @Test
    void newPasswordsRejectUnicodeSpacesAndControlCharacters() {
        for (String whitespace : List.of(" ", "\t", "\n", "\u00a0", "\u2007", "\u202f")) {
            assertThatThrownBy(() -> passwords.validateNewPassword("Strong1!" + whitespace))
                    .isInstanceOfSatisfying(
                            BusinessException.class,
                            ex -> assertThat(ex.code()).isEqualTo(ErrorCode.VALIDATION_ERROR));
        }
    }

    @Test
    void bootstrapAuditFailureRollsBackUserAndRoleBinding() {
        jdbc.execute(
                "ALTER TABLE sys_operation_log ADD CONSTRAINT reject_bootstrap CHECK (action <> 'BOOTSTRAP')");
        assertThatThrownBy(() -> bootstrap.initialize("Admin01", null, "AdminPass1!", null))
                .isInstanceOf(org.springframework.dao.DataAccessException.class);
        assertThat(users.count()).isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_user_role", Integer.class))
                .isZero();
    }

    @Test
    void managementEndpointsUseDatabaseModuleForEveryRole() throws Exception {
        var admin = admin();
        String id = create(admin, "Worker01");
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.parseLong(id));
        jdbc.update("INSERT INTO sys_role(id,code,name) VALUES (991,'ADMIN','ordinary admin')");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES (?,991)", Long.parseLong(id));
        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES (991,1002)");
        var ordinary = login("Worker01", "Initial1!");
        Csrf csrf = csrf(ordinary);
        mvc.perform(
                        post("/user")
                                .session(ordinary)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"username\":\"Worker02\"}"))
                .andExpect(status().isCreated());
        assertThat(users.count()).isEqualTo(3);
        mvc.perform(get("/user/page").session(ordinary)).andExpect(status().isOk());

        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=991");
        mvc.perform(
                        post("/user")
                                .session(ordinary)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"username\":\"Worker03\"}"))
                .andExpect(status().isForbidden());
        assertThat(users.count()).isEqualTo(3);
    }

    @Test
    void userModuleAdministratorCanResetOrdinaryAccountWithOtherModules() throws Exception {
        var admin = admin();
        String managerId = create(admin, "Manager01");
        String targetId = create(admin, "Target01");
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id IN (?,?)",
                Long.parseLong(managerId),
                Long.parseLong(targetId));
        jdbc.update(
                "INSERT INTO sys_role(id,code,name) VALUES (991,'USER_MANAGER','用户管理员'),"
                        + "(992,'MENU_MANAGER','菜单管理员')");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES (?,991),(?,992)",
                Long.parseLong(managerId),
                Long.parseLong(targetId));
        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES (991,1002),(992,1004)");
        var manager = login("Manager01", "Initial1!");
        var previousTargetSession = login("Target01", "Initial1!");
        mvc.perform(get("/auth/me").session(manager))
                .andExpect(jsonPath("$.data.modules.length()").value(1))
                .andExpect(jsonPath("$.data.modules[0]").value("user"));
        mvc.perform(get("/auth/me").session(previousTargetSession))
                .andExpect(jsonPath("$.data.modules[0]").value("menu"));

        Csrf csrf = csrf(manager);
        mvc.perform(
                        post("/user/" + targetId + "/reset-password")
                                .session(manager)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\"}"))
                .andExpect(status().isOk());
        mvc.perform(get("/auth/me").session(previousTargetSession))
                .andExpect(status().isUnauthorized());
        mvc.perform(get("/auth/me").session(login("Target01", "Initial1!")))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.user.mustChangePassword").value(true))
                .andExpect(jsonPath("$.data.modules.length()").value(0));
    }

    @Test
    void superAdminRoleUsesActualMenuRelationForItsModule() throws Exception {
        var admin = admin();
        mvc.perform(get("/user/page").session(admin)).andExpect(status().isOk());
        mvc.perform(get("/auth/me").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.isSuperAdmin").value(true))
                .andExpect(jsonPath("$.data.permissions").doesNotExist());

        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=1 AND menu_id=1002");
        mvc.perform(get("/user/page").session(admin)).andExpect(status().isForbidden());
        mvc.perform(get("/auth/me").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.modules.length()").value(6));

        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES (1,1002)");
        mvc.perform(get("/user/page").session(admin)).andExpect(status().isOk());
    }

    @Test
    void duplicateAndStaleWritesDoNotModifyCurrentUser() throws Exception {
        var admin = admin();
        String id = create(admin, "Worker01");
        Csrf csrf = csrf(admin);
        mvc.perform(
                        post("/user")
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"username\":\"worker01\"}"))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("USERNAME_TAKEN"));
        mvc.perform(
                        put("/user/" + id)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"nickname\":\"First\"}"))
                .andExpect(status().isOk());
        mvc.perform(
                        put("/user/" + id)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"nickname\":\"Stale\"}"))
                .andExpect(status().isConflict());
        assertThat(users.getById(Long.parseLong(id)).getNickname()).isEqualTo("First");
        assertThat(users.count()).isEqualTo(2);
    }

    private MockHttpSession admin() throws Exception {
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        return login("Admin01", "AdminPass1!");
    }

    private String create(MockHttpSession admin, String username) throws Exception {
        Csrf csrf = csrf(admin);
        var result =
                mvc.perform(
                                post("/user")
                                        .session(admin)
                                        .header(csrf.header(), csrf.token())
                                        .contentType("application/json")
                                        .content(
                                                json.writeValueAsString(
                                                        Map.of("username", username))))
                        .andExpect(status().isCreated())
                        .andReturn();
        return json.readTree(result.getResponse().getContentAsString())
                .path("data")
                .path("id")
                .asText();
    }

    private MockHttpSession login(String username, String password) throws Exception {
        Csrf csrf = csrf(null);
        var result =
                mvc.perform(
                                post("/auth/login")
                                        .session(csrf.session())
                                        .header(csrf.header(), csrf.token())
                                        .contentType("application/json")
                                        .content(
                                                json.writeValueAsString(
                                                        Map.of(
                                                                "username",
                                                                username,
                                                                "password",
                                                                password))))
                        .andExpect(status().isOk())
                        .andReturn();
        return (MockHttpSession) result.getRequest().getSession(false);
    }

    private Csrf csrf(MockHttpSession session) throws Exception {
        var request = get("/auth/csrf");
        if (session != null) request.session(session);
        var result = mvc.perform(request).andExpect(status().isOk()).andReturn();
        JsonNode data = json.readTree(result.getResponse().getContentAsString()).path("data");
        return new Csrf(
                (MockHttpSession) result.getRequest().getSession(false),
                data.path("headerName").asText(),
                data.path("token").asText());
    }

    private void adminAction(
            MockHttpSession admin, String id, String action, String version, int expectedStatus)
            throws Exception {
        Csrf csrf = csrf(admin);
        mvc.perform(
                        post("/user/" + id + action)
                                .session(admin)
                                .header(csrf.header(), csrf.token())
                                .contentType("application/json")
                                .content(json.writeValueAsString(Map.of("version", version))))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.status").value(expectedStatus));
    }

    private record Csrf(MockHttpSession session, String header, String token) {}
}
