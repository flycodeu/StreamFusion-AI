package com.streamfusion.platform;

import static com.streamfusion.platform.support.SecureLoginSupport.loginRequest;
import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doThrow;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.csrf;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.support.SecureLoginSupport;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.data.redis.RedisConnectionFailureException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.security.web.context.SecurityContextRepository;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:single_session;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class SingleSessionIntegrationTest extends SecureLoginSupport {
    @Autowired MockMvc mvc;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired PasswordService passwords;
    @Autowired ObjectMapper json;
    @MockitoSpyBean SecurityContextRepository contexts;
    private JdbcTemplate jdbc;
    private long adminId;

    @BeforeEach
    void setup() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        adminId = bootstrap.initialize("SessionAdmin", "会话管理员", "SessionPass1!", null);
        jdbc.update(
                "INSERT INTO sys_user(id,username,nickname,password,status,must_change_password,version,session_version) VALUES (801,'SessionUser','普通用户',?,1,FALSE,0,0)",
                passwords.encode("SessionPass1!"));
    }

    @Test
    void replacementIsExplainedToOnlyTheOldAccountAndDoesNotChangeEditVersion() throws Exception {
        var previous = login("SessionUser");
        var latest = login("SessionUser");
        for (int attempt = 0; attempt < 2; attempt++) {
            mvc.perform(get("/auth/session").session(previous))
                    .andExpect(status().isUnauthorized())
                    .andExpect(jsonPath("$.code").value("SESSION_REPLACED"))
                    .andExpect(jsonPath("$.data.reason").value("REPLACED"))
                    .andExpect(jsonPath("$.data.loginAt").isNotEmpty())
                    .andExpect(jsonPath("$.data.sourceIp").value("127.0.0.1"))
                    .andExpect(jsonPath("$.data.region").value("本机"))
                    .andExpect(jsonPath("$.data.browser").value("Firefox"))
                    .andExpect(jsonPath("$.data.os").value("Windows"))
                    .andExpect(jsonPath("$.data.sessionId").doesNotExist());
        }
        mvc.perform(get("/auth/me").session(previous))
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.code").value("SESSION_REPLACED"));
        mvc.perform(get("/auth/session").session(latest)).andExpect(status().isOk());
        mvc.perform(get("/auth/session"))
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.data").isEmpty());
        assertThat(jdbc.queryForObject("SELECT version FROM sys_user WHERE id=801", Long.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT session_version FROM sys_user WHERE id=801", Long.class))
                .isEqualTo(2);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_login_record WHERE user_id=801 AND end_reason IS NULL",
                                Integer.class))
                .isEqualTo(1);
    }

    @Test
    void failedCredentialsAndFailedSessionSavePreserveTheExistingLogin() throws Exception {
        var previous = login("SessionUser");
        mvc.perform(loginRequest(mvc, json, null, "SessionUser", "WrongPass1!").with(csrf()))
                .andExpect(status().isUnauthorized());
        mvc.perform(get("/auth/session").session(previous)).andExpect(status().isOk());
        doThrow(new RedisConnectionFailureException("synthetic login save failure"))
                .when(contexts)
                .saveContext(any(), any(), any());
        mvc.perform(loginRequest(mvc, json, null, "SessionUser", "SessionPass1!").with(csrf()))
                .andExpect(status().isServiceUnavailable());
        mvc.perform(get("/auth/session").session(previous)).andExpect(status().isOk());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT session_version FROM sys_user WHERE id=801", Long.class))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_login_record WHERE user_id=801",
                                Integer.class))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE actor_id=801 AND action='LOGIN' AND result='SUCCESS'",
                                Integer.class))
                .isEqualTo(1);
    }

    @Test
    void concurrentSuccessfulLoginsLeaveExactlyOneUsableSession() throws Exception {
        try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            var start = new CountDownLatch(1);
            var first =
                    executor.submit(
                            () -> {
                                start.await();
                                return login("SessionUser");
                            });
            var second =
                    executor.submit(
                            () -> {
                                start.await();
                                return login("SessionUser");
                            });
            start.countDown();
            var sessions =
                    List.of(first.get(20, TimeUnit.SECONDS), second.get(20, TimeUnit.SECONDS));
            int active = 0;
            int replaced = 0;
            for (var session : sessions) {
                var response =
                        mvc.perform(get("/auth/session").session(session))
                                .andReturn()
                                .getResponse();
                if (response.getStatus() == 200) active++;
                if (json.readTree(response.getContentAsByteArray())
                        .path("code")
                        .asText()
                        .equals("SESSION_REPLACED")) replaced++;
            }
            assertThat(active).isEqualTo(1);
            assertThat(replaced).isEqualTo(1);
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT session_version FROM sys_user WHERE id=801",
                                    Long.class))
                    .isEqualTo(2);
        }
    }

    @Test
    void forceLogoutRequiresVersionPermissionAndProtectedObjectChecks() throws Exception {
        var admin = login("SessionAdmin");
        var ordinary = login("SessionUser");
        mvc.perform(post("/user/801/force-logout").session(admin).with(csrf()))
                .andExpect(status().isPreconditionRequired());
        mvc.perform(
                        post("/user/801/force-logout")
                                .session(admin)
                                .with(csrf())
                                .header("If-Match", "\"5\""))
                .andExpect(status().isConflict());
        mvc.perform(
                        post("/user/801/force-logout")
                                .session(ordinary)
                                .with(csrf())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isForbidden());
        mvc.perform(
                        post("/user/" + adminId + "/force-logout")
                                .session(admin)
                                .with(csrf())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PROTECTED_ACCOUNT"));
        mvc.perform(
                        post("/user/801/force-logout")
                                .session(admin)
                                .with(csrf())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk())
                .andExpect(header().string("ETag", "\"1\""))
                .andExpect(jsonPath("$.data.status").value(1));
        for (int attempt = 0; attempt < 2; attempt++) {
            mvc.perform(get("/auth/session").session(ordinary))
                    .andExpect(status().isUnauthorized())
                    .andExpect(jsonPath("$.code").value("SESSION_FORCED_LOGOUT"))
                    .andExpect(jsonPath("$.data.reason").value("FORCED_LOGOUT"))
                    .andExpect(jsonPath("$.data.occurredAt").isNotEmpty())
                    .andExpect(jsonPath("$.data.sourceIp").isEmpty());
        }
        assertThat(
                        jdbc.queryForObject(
                                "SELECT end_reason FROM sys_login_record WHERE user_id=801",
                                String.class))
                .isEqualTo("FORCED_LOGOUT");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='USER_FORCE_LOGOUT' AND target_id=801",
                                Integer.class))
                .isEqualTo(1);
        mvc.perform(get("/auth/session").session(login("SessionUser"))).andExpect(status().isOk());
    }

    private MockHttpSession login(String username) throws Exception {
        return (MockHttpSession)
                mvc.perform(
                                loginRequest(mvc, json, null, username, "SessionPass1!")
                                        .with(csrf())
                                        .header(
                                                "User-Agent",
                                                "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0"))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getRequest()
                        .getSession(false);
    }
}
