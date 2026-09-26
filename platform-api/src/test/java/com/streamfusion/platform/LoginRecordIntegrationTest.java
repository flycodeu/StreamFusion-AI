package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.csrf;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.loginrecord.mapper.LoginRecordMapper;
import com.streamfusion.platform.loginrecord.service.LoginRecordService;
import com.streamfusion.platform.support.IdentitySchema;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneId;
import java.util.concurrent.atomic.AtomicReference;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:login_records;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class LoginRecordIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired DataSource source;
    @Autowired ObjectMapper json;
    @Autowired BootstrapService bootstrap;
    @Autowired PasswordService passwords;
    @MockitoBean Clock clock;
    @MockitoSpyBean LoginRecordMapper records;
    private final AtomicReference<Instant> time = new AtomicReference<>();
    private JdbcTemplate jdbc;
    private long adminId;

    @BeforeEach
    void setup() throws Exception {
        time.set(Instant.parse("2026-09-26T01:00:00Z"));
        when(clock.instant()).thenAnswer(call -> time.get());
        when(clock.withZone(any(ZoneId.class)))
                .thenAnswer(call -> Clock.fixed(time.get(), call.getArgument(0)));
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        adminId = bootstrap.initialize("HistoryAdmin", "原始昵称", "HistoryPass1!", null);
    }

    @Test
    void capturesTrustedSourceClientAndHistoricalIdentityWithoutSessionCredentials()
            throws Exception {
        var session = login("HistoryAdmin");
        jdbc.update("UPDATE sys_user SET nickname='后续昵称' WHERE id=?", adminId);
        var response =
                mvc.perform(get("/auth/login-records/page").session(session))
                        .andExpect(status().isOk())
                        .andExpect(jsonPath("$.data.total").value(1))
                        .andExpect(jsonPath("$.data.items[0].nickname").value("原始昵称"))
                        .andExpect(jsonPath("$.data.items[0].sourceIp").value("192.168.1.21"))
                        .andExpect(jsonPath("$.data.items[0].region").value("内网"))
                        .andExpect(jsonPath("$.data.items[0].browser").value("Edge"))
                        .andExpect(jsonPath("$.data.items[0].os").value("Windows"))
                        .andExpect(jsonPath("$.data.items[0].status").value("ACTIVE"))
                        .andExpect(jsonPath("$.data.items[0].durationSeconds").value(0))
                        .andReturn()
                        .getResponse()
                        .getContentAsString();
        assertThat(response)
                .doesNotContain(
                        json.writeValueAsString(session.getId()),
                        "sessionId",
                        "HistoryPass1!",
                        "sessionVersion",
                        "password");
        mvc.perform(get("/auth/login-records/page")).andExpect(status().isUnauthorized());
        mvc.perform(get("/auth/login-records/page?size=101").session(session))
                .andExpect(status().isBadRequest());
    }

    @Test
    void throttlesActivityAndRecordsAnExplicitLogoutDuration() throws Exception {
        var session = login("HistoryAdmin");
        clearInvocations(records);
        for (int second : new int[] {1, 10, 59}) {
            time.set(Instant.parse("2026-09-26T01:00:00Z").plusSeconds(second));
            mvc.perform(get("/auth/me").session(session)).andExpect(status().isOk());
        }
        verify(records, never()).touch(anyLong(), any(), any());
        time.set(Instant.parse("2026-09-26T01:01:00Z"));
        mvc.perform(get("/auth/me").session(session)).andExpect(status().isOk());
        verify(records, times(1)).touch(anyLong(), any(), any());
        time.set(Instant.parse("2026-09-26T01:01:20Z"));
        mvc.perform(post("/auth/logout").session(session).with(csrf())).andExpect(status().isOk());
        var fresh = login("HistoryAdmin");
        var old = items(fresh).get(1);
        assertThat(old.path("endReason").asText()).isEqualTo("LOGOUT");
        assertThat(old.path("status").asText()).isEqualTo("ENDED");
        assertThat(old.path("durationSeconds").asLong()).isEqualTo(80);
    }

    @Test
    void idleAndAbsoluteExpiryUseLastRecordedActivityWithoutClaimingOnlineTime() throws Exception {
        login("HistoryAdmin");
        time.set(time.get().plusSeconds(1861));
        var second = login("HistoryAdmin");
        var idle = items(second).get(1);
        assertThat(idle.path("status").asText()).isEqualTo("EXPIRED");
        assertThat(idle.path("endReason").asText()).isEqualTo("IDLE_TIMEOUT");
        assertThat(idle.path("durationSeconds").asLong()).isZero();
        jdbc.update(
                "UPDATE sys_login_record SET last_activity_at=TIMESTAMPADD(SECOND,28799,login_at)");
        time.set(Instant.parse("2026-09-26T09:00:01Z"));
        var latest = login("HistoryAdmin");
        var oldest = items(latest).get(2);
        assertThat(oldest.path("endReason").asText()).isEqualTo("ABSOLUTE_TIMEOUT");
        assertThat(oldest.path("status").asText()).isEqualTo("EXPIRED");
    }

    @Test
    void userManagersCannotReadOtherSuperAdministratorsAndHistoryDoesNotFollowReusedAccounts()
            throws Exception {
        var admin = login("HistoryAdmin");
        insertOrdinary(501, "HistoryUser");
        var ordinary = login("HistoryUser");
        mvc.perform(get("/user/" + adminId + "/login-records/page").session(ordinary))
                .andExpect(status().isForbidden());
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status) VALUES (502,'HISTORY_ADMIN','历史管理','ENABLED')");
        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES (502,1002)");
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES (501,502)");
        mvc.perform(get("/user/" + adminId + "/login-records/page").session(ordinary))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PROTECTED_ACCOUNT"));
        mvc.perform(get("/user/501/login-records/page").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.total").value(1));
        mvc.perform(delete("/user/501").session(admin).with(csrf()).header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT end_reason FROM sys_login_record WHERE user_id=501",
                                String.class))
                .isEqualTo("ACCOUNT_DELETED");
        insertOrdinary(503, "HistoryUser");
        assertThat(items(login("HistoryUser")).size()).isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_login_record WHERE user_id=501",
                                Integer.class))
                .isEqualTo(1);
    }

    @Test
    void accountCooldownIsSeparateFromManualDisableAndResetClosesActiveHistory() throws Exception {
        var admin = login("HistoryAdmin");
        insertOrdinary(511, "HistoryUser");
        var ordinary = login("HistoryUser");
        for (int i = 0; i < 5; i++) {
            mvc.perform(
                            post("/auth/login")
                                    .with(csrf())
                                    .contentType("application/json")
                                    .content(
                                            "{\"username\":\"HistoryUser\",\"password\":\"WrongPass1!\"}"))
                    .andExpect(status().isUnauthorized());
        }
        mvc.perform(get("/user/511").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.status").value(1))
                .andExpect(jsonPath("$.data.loginRestricted").value(true))
                .andExpect(jsonPath("$.data.lockedUntil").value("2026-09-26T01:15:00Z"));
        mvc.perform(get("/user/page?keyword=HistoryUser").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.items[0].loginRestricted").value(true));
        mvc.perform(
                        post("/user/511/reset-password")
                                .session(admin)
                                .with(csrf())
                                .contentType("application/json")
                                .content("{\"version\":\"0\"}"))
                .andExpect(status().isOk());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT end_reason FROM sys_login_record WHERE user_id=511",
                                String.class))
                .isEqualTo("PASSWORD_RESET");
        mvc.perform(get("/auth/me").session(ordinary)).andExpect(status().isUnauthorized());
    }

    @Test
    void failedLoginsStayInAuditAndOldSessionsRemainCompatible() throws Exception {
        mvc.perform(
                        post("/auth/login")
                                .with(csrf())
                                .contentType("application/json")
                                .content(
                                        "{\"username\":\"HistoryAdmin\",\"password\":\"WrongPass1!\"}"))
                .andExpect(status().isUnauthorized());
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_login_record", Integer.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='LOGIN' AND result='FAILURE'",
                                Integer.class))
                .isEqualTo(1);
        var session = login("HistoryAdmin");
        session.removeAttribute(LoginRecordService.SESSION_ATTRIBUTE);
        mvc.perform(get("/auth/me").session(session)).andExpect(status().isOk());
    }

    @Test
    void laterAccountResetAndDisableDoNotRewriteAnEarlierIdleExpiry() throws Exception {
        var admin = login("HistoryAdmin");
        insertOrdinary(521, "HistoryUser");
        login("HistoryUser");
        time.set(time.get().plusSeconds(1861));
        mvc.perform(
                        post("/user/521/reset-password")
                                .session(admin)
                                .with(csrf())
                                .contentType("application/json")
                                .content("{\"version\":\"0\"}"))
                .andExpect(status().isOk());
        mvc.perform(
                        post("/user/521/disable")
                                .session(admin)
                                .with(csrf())
                                .contentType("application/json")
                                .content("{\"version\":\"1\"}"))
                .andExpect(status().isOk());
        mvc.perform(get("/user/521/login-records/page").session(admin))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.items[0].status").value("EXPIRED"))
                .andExpect(jsonPath("$.data.items[0].endReason").value("IDLE_TIMEOUT"))
                .andExpect(jsonPath("$.data.items[0].durationSeconds").value(0));
    }

    private void insertOrdinary(long id, String username) {
        jdbc.update(
                "INSERT INTO sys_user(id,username,nickname,password,status,must_change_password,session_version,version) VALUES (?,?,?,?,1,FALSE,0,0)",
                id,
                username,
                "普通用户",
                passwords.encode("HistoryPass1!"));
    }

    private MockHttpSession login(String username) throws Exception {
        return (MockHttpSession)
                mvc.perform(
                                post("/auth/login")
                                        .with(csrf())
                                        .with(
                                                request -> {
                                                    request.setRemoteAddr("192.168.1.21");
                                                    return request;
                                                })
                                        .header("X-Forwarded-For", "8.8.8.8")
                                        .header(
                                                "User-Agent",
                                                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/128.0 Safari/537.36 Edg/128.0")
                                        .contentType("application/json")
                                        .content(
                                                json.writeValueAsString(
                                                        java.util.Map.of(
                                                                "username",
                                                                username,
                                                                "password",
                                                                "HistoryPass1!"))))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getRequest()
                        .getSession(false);
    }

    private JsonNode items(MockHttpSession session) throws Exception {
        return json.readTree(
                        mvc.perform(get("/auth/login-records/page").session(session))
                                .andExpect(status().isOk())
                                .andReturn()
                                .getResponse()
                                .getContentAsByteArray())
                .path("data")
                .path("items");
    }
}
