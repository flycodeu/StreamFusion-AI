package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.csrf;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.support.IdentitySchema;
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
            "spring.datasource.url=jdbc:h2:mem:version_boundary;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class VersionBoundaryIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    private JdbcTemplate jdbc;
    private MockHttpSession admin;

    @BeforeEach
    void setup() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        bootstrap.initialize("VersionAdmin", "版本测试", "VersionAdmin1!", null);
        admin =
                (MockHttpSession)
                        mvc.perform(
                                        post("/auth/login")
                                                .with(csrf())
                                                .contentType("application/json")
                                                .content(
                                                        "{\"username\":\"VersionAdmin\",\"password\":\"VersionAdmin1!\"}"))
                                .andExpect(status().isOk())
                                .andReturn()
                                .getRequest()
                                .getSession(false);
        jdbc.update(
                "INSERT INTO sys_user(id,username,password,nickname,status,must_change_password) VALUES (9001,'BoundaryUser','unused-test-hash','原昵称',1,FALSE)");
    }

    @Test
    void exhaustedEditVersionNeverWrapsOrOverwritesAnExistingRecord() throws Exception {
        jdbc.update("UPDATE sys_user SET version=? WHERE id=9001", Long.MAX_VALUE);
        mvc.perform(
                        put("/user/9001")
                                .session(admin)
                                .with(csrf())
                                .contentType("application/json")
                                .content(
                                        "{\"version\":\"9223372036854775807\",\"nickname\":\"不能覆盖\",\"gender\":0}"))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("VERSION_EXHAUSTED"));
        assertThat(jdbc.queryForObject("SELECT version FROM sys_user WHERE id=9001", Long.class))
                .isEqualTo(Long.MAX_VALUE);
        assertThat(jdbc.queryForObject("SELECT nickname FROM sys_user WHERE id=9001", String.class))
                .isEqualTo("原昵称");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE target_id=9001",
                                Integer.class))
                .isZero();
    }

    @Test
    void exhaustedSessionVersionCannotBeResetByDisablingOrResettingPassword() throws Exception {
        jdbc.update("UPDATE sys_user SET session_version=? WHERE id=9001", Long.MAX_VALUE);
        for (String action : new String[] {"disable", "reset-password"}) {
            mvc.perform(
                            post("/user/9001/" + action)
                                    .session(admin)
                                    .with(csrf())
                                    .contentType("application/json")
                                    .content("{\"version\":\"0\"}"))
                    .andExpect(status().isConflict())
                    .andExpect(jsonPath("$.code").value("VERSION_EXHAUSTED"));
        }
        assertThat(
                        jdbc.queryForObject(
                                "SELECT session_version FROM sys_user WHERE id=9001", Long.class))
                .isEqualTo(Long.MAX_VALUE);
        assertThat(jdbc.queryForObject("SELECT status FROM sys_user WHERE id=9001", Integer.class))
                .isEqualTo(1);
        assertThat(jdbc.queryForObject("SELECT password FROM sys_user WHERE id=9001", String.class))
                .isEqualTo("unused-test-hash");
    }

    @Test
    void exhaustedIpGenerationDoesNotReuseZeroOrLoseTheBlock() throws Exception {
        jdbc.update(
                "INSERT INTO sys_ip_block(id,source_ip,status,reason_code,failed_attempts,window_seconds,blocked_at,version) VALUES(9002,'192.0.2.90','BLOCKED','LOGIN_FAILURE_THRESHOLD',20,600,CURRENT_TIMESTAMP,?)",
                Long.MAX_VALUE);
        mvc.perform(
                        put("/audit/ip-blocks/9002/unblock")
                                .session(admin)
                                .with(csrf())
                                .header("If-Match", "\"9223372036854775807\""))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("VERSION_EXHAUSTED"));
        assertThat(
                        jdbc.queryForObject(
                                "SELECT status FROM sys_ip_block WHERE id=9002", String.class))
                .isEqualTo("BLOCKED");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT version FROM sys_ip_block WHERE id=9002", Long.class))
                .isEqualTo(Long.MAX_VALUE);
    }
}
