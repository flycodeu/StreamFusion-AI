package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import com.streamfusion.platform.server.service.ServerCollector;
import com.streamfusion.platform.support.IdentitySchema;
import java.time.Instant;
import java.util.List;
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
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.MvcResult;

@SpringBootTest(
        properties = "spring.datasource.url=jdbc:h2:mem:admin_read;MODE=MySQL;DB_CLOSE_DELAY=-1")
@AutoConfigureMockMvc
@ActiveProfiles("test")
class AdminReadIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @MockitoBean ServerCollector collector;
    private JdbcTemplate jdbc;
    private long adminId;
    private MockHttpSession session;

    @BeforeEach
    void setup() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        adminId = bootstrap.initialize("ReadAdmin", "审计管理员", "ReadAdmin1!", null);
        MvcResult csrfResult = mvc.perform(get("/auth/csrf")).andReturn();
        JsonNode csrf = json.readTree(csrfResult.getResponse().getContentAsString()).path("data");
        session = (MockHttpSession) csrfResult.getRequest().getSession(false);
        mvc.perform(
                        post("/auth/login")
                                .session(session)
                                .header(
                                        csrf.path("headerName").asText(),
                                        csrf.path("token").asText())
                                .contentType("application/json")
                                .content(
                                        "{\"username\":\"ReadAdmin\",\"password\":\"ReadAdmin1!\"}"))
                .andExpect(status().isOk());
        when(collector.collect())
                .thenReturn(
                        new ServerSnapshotVo(
                                Instant.now(),
                                false,
                                "READY",
                                new ServerSnapshotVo.Host("Test OS", "test", 2, 10.0, 1000L, 500L),
                                new ServerSnapshotVo.Jvm(
                                        "21", "100", 1.0, 100, 1000, 50, 5000, Instant.now()),
                                List.of(
                                        new ServerSnapshotVo.ProjectService(
                                                "api",
                                                "Platform API",
                                                "127.0.0.1",
                                                8080,
                                                "RUNNING")),
                                "UNAVAILABLE",
                                List.of()));
    }

    @Test
    void requiresLoginAndSeparateModuleGrants() throws Exception {
        mvc.perform(get("/audit/page"))
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.code").value("UNAUTHORIZED"));
        mvc.perform(get("/server/status"))
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.code").value("UNAUTHORIZED"));
        mvc.perform(get("/api-docs/status"))
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.code").value("UNAUTHORIZED"));
        jdbc.update("DELETE FROM sys_role_menu WHERE menu_id IN (1006,1007,1009)");
        mvc.perform(get("/audit/page").session(session)).andExpect(status().isForbidden());
        mvc.perform(get("/server/status").session(session)).andExpect(status().isForbidden());
        mvc.perform(get("/api-docs/status").session(session))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"));
        mvc.perform(get("/user/page").session(session)).andExpect(status().isOk());
    }

    @Test
    void documentationStatusRequiresItsPageAndEnabledAncestors() throws Exception {
        mvc.perform(get("/api-docs/status").session(session))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value("SUCCESS"))
                .andExpect(jsonPath("$.data.enabled").value(true));
        mvc.perform(get("/auth/me").session(session))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.routes[1].name").value("监控面板"))
                .andExpect(jsonPath("$.data.routes[1].children[0].moduleKey").value("api-docs"))
                .andExpect(jsonPath("$.data.routes[1].children[0].path").value("/monitor/ApiDocs"));
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE id=1008");
        mvc.perform(get("/api-docs/status").session(session))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"));
    }

    @Test
    void pagesFiltersConvertsShanghaiTimeAndSanitizesDetail() throws Exception {
        insert(
                9007199254740993L,
                adminId,
                "{\"name\":\"改名\",\"roleIds\":[\"123\"],\"password\":\"never-expose\",\"token\":\"never-expose\"}");
        mvc.perform(
                        get("/audit/page")
                                .session(session)
                                .param("user", "ReadAdmin")
                                .param("action", "EDIT")
                                .param("module", "USER")
                                .param("result", "SUCCESS")
                                .param("size", "1")
                                .param("startTime", "2025-01-02T00:00:00Z")
                                .param("endTime", "2025-01-02T00:00:01Z"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.total").value(1))
                .andExpect(jsonPath("$.data.items[0].id").value("9007199254740993"))
                .andExpect(jsonPath("$.data.items[0].createdAt").value("2025-01-02T00:00:00Z"))
                .andExpect(jsonPath("$.data.items[0].changes").doesNotExist());
        String body =
                mvc.perform(get("/audit/9007199254740993").session(session))
                        .andExpect(status().isOk())
                        .andExpect(jsonPath("$.data.changes.name").value("改名"))
                        .andExpect(jsonPath("$.data.changes.roleIds[0]").value("123"))
                        .andExpect(jsonPath("$.data.changes.password").doesNotExist())
                        .andReturn()
                        .getResponse()
                        .getContentAsString();
        assertThat(body).doesNotContain("never-expose", "clientSummary");
    }

    @Test
    void preservesDeletedActorIdentityAndRejectsInvalidQuery() throws Exception {
        insert(9007199254740994L, 999L, "{}");
        mvc.perform(get("/audit/page").session(session).param("user", "999"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.total").value(1))
                .andExpect(jsonPath("$.data.items[0].actorId").value("999"))
                .andExpect(jsonPath("$.data.items[0].username").isEmpty());
        mvc.perform(get("/audit/page").session(session).param("size", "101"))
                .andExpect(status().isBadRequest());
        mvc.perform(
                        get("/audit/page")
                                .session(session)
                                .param("startTime", "2025-01-03T00:00:00Z")
                                .param("endTime", "2025-01-02T00:00:00Z"))
                .andExpect(status().isBadRequest());
        mvc.perform(get("/audit/page").session(session).param("result", "ALL"))
                .andExpect(status().isBadRequest());
        mvc.perform(get("/audit/page").session(session).param("traceId", "partial-trace"))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
        mvc.perform(get("/audit/999999").session(session)).andExpect(status().isNotFound());
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE id=1001");
        mvc.perform(get("/audit/page").session(session)).andExpect(status().isForbidden());
    }

    @Test
    void asyncServerResponsePreservesRequestTraceAndSafeMetricFields() throws Exception {
        MvcResult pending =
                mvc.perform(get("/server/status").session(session))
                        .andExpect(request().asyncStarted())
                        .andReturn();
        String trace = pending.getResponse().getHeader("X-Trace-Id");
        mvc.perform(asyncDispatch(pending))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.traceId").value(trace))
                .andExpect(jsonPath("$.data.state").value("READY"))
                .andExpect(jsonPath("$.data.services[0].port").value(8080))
                .andExpect(jsonPath("$.data.jvm.commandLine").doesNotExist())
                .andExpect(jsonPath("$.data.jvm.environment").doesNotExist());
    }

    @Test
    void asyncServerCompletionStillRejectsARevokedModule() throws Exception {
        MvcResult pending =
                mvc.perform(get("/server/status").session(session))
                        .andExpect(request().asyncStarted())
                        .andReturn();
        jdbc.update("DELETE FROM sys_role_menu WHERE menu_id=1007");
        mvc.perform(asyncDispatch(pending))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"));
    }

    private void insert(long id, long actor, String changes) {
        jdbc.update(
                "INSERT INTO sys_operation_log(id,actor_id,target_type,target_id,action,result,changes,source_ip,created_at) VALUES(?,?,'USER',?,'EDIT','SUCCESS',?,'127.0.0.1',TIMESTAMP '2025-01-02 08:00:00')",
                id,
                actor,
                actor,
                changes);
    }
}
