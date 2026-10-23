package com.streamfusion.platform.monitor.security;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.support.SecureLoginSupport;
import com.streamfusion.platform.system.auth.service.BootstrapService;
import com.streamfusion.platform.system.auth.service.PasswordService;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:security_alerts;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.security-monitor.enabled=true",
            "streamfusion.work-dir=target/security-alert-test-${random.uuid}"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class SecurityAlertsIntegrationTest extends SecureLoginSupport {
    @Autowired MockMvc mvc;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired PasswordService passwords;
    @Autowired ObjectMapper json;
    @Autowired SecurityMonitor monitor;
    private JdbcTemplate jdbc;
    private long admin;

    @BeforeEach
    void setup() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        admin = bootstrap.initialize("SecurityAdmin", "安全管理员", "SecurityPass1!", null);
        jdbc.update(
                "INSERT INTO sys_user(id,username,nickname,password,status,must_change_password,version,session_version) VALUES (801,'SecurityUser','普通用户',?,1,FALSE,0,0)",
                passwords.encode("SecurityPass1!"));
    }

    private MockHttpSession login(String username) throws Exception {
        var prepared = mvc.perform(get("/auth/csrf")).andReturn();
        var token = json.readTree(prepared.getResponse().getContentAsString()).path("data");
        var session = (MockHttpSession) prepared.getRequest().getSession(false);
        mvc.perform(
                        loginRequest(mvc, json, session, username, "SecurityPass1!")
                                .header(
                                        token.path("headerName").asText(),
                                        token.path("token").asText()))
                .andExpect(status().isOk());
        return session;
    }

    @Test
    void anonymousProbesCreateEvidenceButCannotReadIt() throws Exception {
        for (int i = 0; i < 12; i++)
            mvc.perform(get("/unknown-private-path-" + i)).andExpect(status().isUnauthorized());
        monitor.tick();
        assertThat(monitor.page(1, 100).items())
                .anySatisfy(
                        a -> {
                            assertThat(a.rule()).isEqualTo(SecurityAlert.Rule.PATH_SCAN);
                            assertThat(a.traceIds()).isNotEmpty();
                        });
        for (String path : new String[] {"/page", "/feed"}) {
            mvc.perform(get("/audit/security-alerts" + path)).andExpect(status().isUnauthorized());
        }
        var session = login("SecurityAdmin");
        mvc.perform(get("/audit/security-alerts/page").session(session))
                .andExpect(status().isOk())
                .andExpect(header().string("Cache-Control", "no-store"))
                .andExpect(jsonPath("$.data.total").isNumber())
                .andExpect(jsonPath("$.data.items[0].sourceIp").isNotEmpty());
        mvc.perform(get("/audit/security-alerts/feed").session(session))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.enabled").value(true));
        mvc.perform(head("/audit/security-alerts/feed").session(session))
                .andExpect(status().isOk())
                .andExpect(content().string(""));
        mvc.perform(post("/audit/security-alerts/feed").session(session))
                .andExpect(status().isMethodNotAllowed());
        mvc.perform(get("/audit/security-alerts/page").param("size", "100000").session(session))
                .andExpect(status().isBadRequest());
    }

    @Test
    void moduleGrantAloneAndSuperRoleAloneAreInsufficientAndRevocationIsImmediate()
            throws Exception {
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) SELECT 801,id FROM sys_role WHERE code='ADMIN'");
        var ordinary = login("SecurityUser");
        for (String path : new String[] {"/page", "/feed"})
            mvc.perform(get("/audit/security-alerts" + path).session(ordinary))
                    .andExpect(status().isForbidden());
        var session = login("SecurityAdmin");
        mvc.perform(get("/audit/security-alerts/feed").session(session)).andExpect(status().isOk());
        jdbc.update(
                "DELETE FROM sys_role_menu WHERE menu_id IN (SELECT id FROM sys_menu WHERE module_key='audit')");
        for (String path : new String[] {"/page", "/feed"})
            mvc.perform(get("/audit/security-alerts" + path).session(session))
                    .andExpect(status().isForbidden());
    }

    @Test
    void pendingPasswordAndRevokedSessionsCannotReadFeed() throws Exception {
        var session = login("SecurityAdmin");
        jdbc.update("UPDATE sys_user SET must_change_password=TRUE WHERE id=?", admin);
        mvc.perform(get("/audit/security-alerts/feed").session(session))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PASSWORD_CHANGE_REQUIRED"));
        jdbc.update("UPDATE sys_user SET session_version=session_version+1 WHERE id=?", admin);
        mvc.perform(get("/audit/security-alerts/feed").session(session))
                .andExpect(status().isUnauthorized());
    }
}
