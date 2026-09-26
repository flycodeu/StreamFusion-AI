package com.streamfusion.platform;

import static com.streamfusion.platform.support.SecureLoginSupport.claimEachChallengeOnce;
import static com.streamfusion.platform.support.SecureLoginSupport.loginRequest;
import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.csrf;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.auth.guard.IpBlockService;
import com.streamfusion.platform.auth.guard.LoginGuardStore;
import com.streamfusion.platform.auth.guard.mapper.IpBlockMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.support.IdentitySchema;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.dao.DataAccessResourceFailureException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.request.RequestPostProcessor;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:ip_guard;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.ip-guard.enabled=true",
            "platform.ip-guard.trusted-proxies=127.0.0.1",
            "platform.ip-guard.failure-threshold=5"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class IpGuardIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired IpBlockService blocks;
    @MockitoBean LoginGuardStore store;
    @MockitoSpyBean IpBlockMapper mapper;
    private JdbcTemplate jdbc;
    private MockHttpSession admin;
    private long adminId;

    @BeforeEach
    void setup() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        claimEachChallengeOnce(store);
        var counters = new ConcurrentHashMap<String, AtomicLong>();
        when(store.failure(anyString(), anyLong()))
                .thenAnswer(
                        call ->
                                new LoginGuardStore.Counter(
                                        counters.computeIfAbsent(
                                                        call.getArgument(0)
                                                                + ":"
                                                                + call.getArgument(1),
                                                        key -> new AtomicLong())
                                                .incrementAndGet(),
                                        600));
        when(store.resource(anyString())).thenReturn(new LoginGuardStore.Counter(1, 60));
        adminId = bootstrap.initialize("GuardAdmin", "安全管理员", "GuardAdmin1!", null);
        var result =
                mvc.perform(
                                loginRequest(mvc, json, null, "GuardAdmin", "GuardAdmin1!")
                                        .with(csrf())
                                        .with(ip("192.0.2.1")))
                        .andExpect(status().isOk())
                        .andReturn();
        admin = (MockHttpSession) result.getRequest().getSession(false);
    }

    @Test
    void unknownAccountsAndBadCipherCountOnceThenBlockEveryBusinessRequestIncludingSuperAdmin()
            throws Exception {
        String attacker = "192.0.2.10";
        for (int i = 0; i < 4; i++) {
            if (i == 2) {
                var freshLogin =
                        mvc.perform(
                                        loginRequest(mvc, json, null, "GuardAdmin", "GuardAdmin1!")
                                                .with(csrf())
                                                .with(ip(attacker)))
                                .andExpect(status().isOk())
                                .andReturn();
                admin = (MockHttpSession) freshLogin.getRequest().getSession(false);
            }
            mvc.perform(
                            loginRequest(mvc, json, null, "MissingUser", "Wrong1!")
                                    .with(csrf())
                                    .with(ip(attacker)))
                    .andExpect(status().isUnauthorized());
        }
        mvc.perform(
                        post("/auth/login/secure")
                                .with(csrf())
                                .with(ip(attacker))
                                .contentType("application/json")
                                .content("{}"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("IP_BLOCKED"));
        assertThat(count("SELECT COUNT(*) FROM sys_ip_block WHERE status='BLOCKED'")).isEqualTo(1);
        assertThat(count("SELECT COUNT(*) FROM sys_operation_log WHERE action='IP_BLOCK_CREATE'"))
                .isEqualTo(1);
        for (int i = 0; i < 3; i++)
            mvc.perform(get("/audit/page").session(admin).with(ip(attacker)))
                    .andExpect(status().isForbidden())
                    .andExpect(jsonPath("$.code").value("IP_BLOCKED"));
        mvc.perform(get("/auth/login/challenge").with(ip(attacker)))
                .andExpect(status().isForbidden());
        verify(store, times(5)).failure(eq(attacker), eq(0L));
        assertThat(count("SELECT COUNT(*) FROM sys_operation_log WHERE action='IP_BLOCK_CREATE'"))
                .isEqualTo(1);
        mvc.perform(get("/auth/csrf").session(admin).with(ip(attacker))).andExpect(status().isOk());
        mvc.perform(get("/actuator/health").with(ip(attacker))).andExpect(status().isOk());
        mvc.perform(post("/auth/logout").session(admin).with(csrf()).with(ip(attacker)))
                .andExpect(status().isOk());
    }

    @Test
    void blockedClientCanLogoutBehindTrustedProxyAndKeepsItsAuditSource() throws Exception {
        String clientIp = "192.0.2.66";
        blocks.block(clientIp, 0, 5, new AuditContextDto(clientIp, "Test"));
        mvc.perform(
                        post("/auth/logout")
                                .session(admin)
                                .with(csrf())
                                .with(ip("127.0.0.1"))
                                .header("X-Forwarded-For", clientIp))
                .andExpect(status().isOk());
        assertThat(admin.isInvalid()).isTrue();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT source_ip FROM sys_operation_log WHERE action='LOGOUT'",
                                String.class))
                .isEqualTo(clientIp);
    }

    @Test
    void unblocksWithVersionAndPreventsOldInflightFailureFromReblockingReleasedAddress()
            throws Exception {
        String attacker = "2001:db8:0:0:0:0:0:10";
        blocks.block(attacker, 0, 5, new AuditContextDto(attacker, "Test"));
        JsonNode row =
                json.readTree(
                                mvc.perform(
                                                get("/audit/ip-blocks/page")
                                                        .session(admin)
                                                        .param("sourceIp", "2001:db8::10"))
                                        .andExpect(status().isOk())
                                        .andExpect(jsonPath("$.data.total").value(1))
                                        .andReturn()
                                        .getResponse()
                                        .getContentAsString())
                        .path("data")
                        .path("items")
                        .get(0);
        assertThat(row.path("version").isTextual()).isTrue();
        String path = "/audit/ip-blocks/" + row.path("id").asText() + "/unblock";
        mvc.perform(put(path).session(admin).with(csrf()))
                .andExpect(status().isPreconditionRequired());
        mvc.perform(put(path).session(admin).with(csrf()).header("If-Match", "\"0\""))
                .andExpect(status().isConflict());
        mvc.perform(put(path).session(admin).with(csrf()).header("If-Match", "\"1\""))
                .andExpect(status().isOk())
                .andExpect(header().string("ETag", "\"2\""))
                .andExpect(jsonPath("$.data.status").value("RELEASED"))
                .andExpect(jsonPath("$.data.unblockedBy").value(Long.toString(adminId)))
                .andExpect(jsonPath("$.data.unblockedByReference.id").value(Long.toString(adminId)))
                .andExpect(jsonPath("$.data.unblockedByReference.name").value("安全管理员"))
                .andExpect(jsonPath("$.data.unblockedByReference.source").value("SNAPSHOT"));
        assertThat(blocks.requireAllowed(attacker)).isEqualTo(2);
        blocks.block(attacker, 0, 99, new AuditContextDto(attacker, "late request"));
        assertThat(blocks.requireAllowed(attacker)).isEqualTo(2);
        assertThat(count("SELECT COUNT(*) FROM sys_operation_log WHERE action='IP_BLOCK_RELEASE'"))
                .isEqualTo(1);
    }

    @Test
    void ordinaryAuditManagerCannotListOrUnblockAndRevokedAuditStillDeniesSuperAdmin()
            throws Exception {
        blocks.block("192.0.2.20", 0, 5, new AuditContextDto("192.0.2.20", null));
        long id = jdbc.queryForObject("SELECT id FROM sys_ip_block", Long.class);
        mvc.perform(get("/audit/ip-blocks/page")).andExpect(status().isUnauthorized());
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(9001,'AUDITOR','审计员','ENABLED',0)");
        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES(9001,1006)");
        jdbc.update("DELETE FROM sys_user_role WHERE user_id=?", adminId);
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,9001)", adminId);
        mvc.perform(get("/audit/page").session(admin)).andExpect(status().isOk());
        mvc.perform(get("/audit/ip-blocks/page").session(admin)).andExpect(status().isForbidden());
        mvc.perform(
                        put("/audit/ip-blocks/" + id + "/unblock")
                                .session(admin)
                                .with(csrf())
                                .header("If-Match", "\"1\""))
                .andExpect(status().isForbidden());
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,1)", adminId);
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE id=1006");
        mvc.perform(get("/audit/ip-blocks/page").session(admin)).andExpect(status().isForbidden());
    }

    @Test
    void keepsTheLatestReleaseActorNameAfterRenameAndHardDelete() throws Exception {
        jdbc.update(
                "INSERT INTO sys_user(id,username,nickname,password,status,must_change_password)"
                        + " SELECT 101,'FormerAdmin','首次解封人',password,1,FALSE FROM sys_user WHERE id=?",
                adminId);
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(101,1)");
        var login =
                mvc.perform(
                                loginRequest(mvc, json, null, "FormerAdmin", "GuardAdmin1!")
                                        .with(csrf()))
                        .andExpect(status().isOk())
                        .andReturn();
        var former = (MockHttpSession) login.getRequest().getSession(false);
        String ip = "192.0.2.71";
        blocks.block(ip, 0, 5, new AuditContextDto(ip, null));
        long id =
                jdbc.queryForObject(
                        "SELECT id FROM sys_ip_block WHERE source_ip=?", Long.class, ip);
        String release = "/audit/ip-blocks/" + id + "/unblock";
        mvc.perform(put(release).session(former).with(csrf()).header("If-Match", "\"1\""))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.unblockedByReference.name").value("首次解封人"));
        jdbc.update("UPDATE sys_user SET nickname='最近解封人' WHERE id=101");
        blocks.block(ip, 2, 5, new AuditContextDto(ip, null));
        mvc.perform(get("/audit/ip-blocks/page").session(admin).param("sourceIp", ip))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.items[0].unblockedByReference").isEmpty());
        mvc.perform(put(release).session(former).with(csrf()).header("If-Match", "\"3\""))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.unblockedByReference.name").value("最近解封人"));
        jdbc.update("UPDATE sys_user SET nickname='后来改名' WHERE id=101");
        for (boolean delete : java.util.List.of(false, true)) {
            if (delete) {
                jdbc.update("DELETE FROM sys_user_role WHERE user_id=101");
                jdbc.update("DELETE FROM sys_user WHERE id=101");
            }
            String body =
                    mvc.perform(get("/audit/ip-blocks/page").session(admin).param("sourceIp", ip))
                            .andExpect(status().isOk())
                            .andExpect(jsonPath("$.data.items[0].unblockedBy").value("101"))
                            .andExpect(
                                    jsonPath("$.data.items[0].unblockedByReference.id")
                                            .value("101"))
                            .andExpect(
                                    jsonPath("$.data.items[0].unblockedByReference.name")
                                            .value("最近解封人"))
                            .andExpect(
                                    jsonPath("$.data.items[0].unblockedByReference.code")
                                            .value("FormerAdmin"))
                            .andExpect(
                                    jsonPath("$.data.items[0].unblockedByReference.source")
                                            .value("SNAPSHOT"))
                            .andReturn()
                            .getResponse()
                            .getContentAsString();
            assertThat(body).doesNotContain("首次解封人", "后来改名", "changes", "password");
        }
    }

    @Test
    void oldReleaseRowsUseCurrentOrMissingNamesWithoutBorrowingAnEarlierSnapshot()
            throws Exception {
        jdbc.update(
                "INSERT INTO sys_user(id,username,nickname,password) VALUES(101,'ExistingUser','当前解封人','synthetic-only')");
        jdbc.update(
                "INSERT INTO sys_ip_block(id,source_ip,status,reason_code,failed_attempts,window_seconds,blocked_at,unblocked_at,unblocked_by,version)"
                        + " VALUES(301,'192.0.2.72','RELEASED','LOGIN_FAILURE_THRESHOLD',5,600,'2026-01-01 00:00:00','2026-01-01 00:01:00',101,2),"
                        + "(302,'192.0.2.73','RELEASED','LOGIN_FAILURE_THRESHOLD',5,600,'2026-01-01 00:00:00','2026-01-01 00:01:00',102,2),"
                        + "(303,'192.0.2.74','RELEASED','LOGIN_FAILURE_THRESHOLD',5,600,'2026-01-01 00:00:00','2026-01-01 00:01:00',101,2)");
        String oldSnapshot =
                "{\"_actor\":{\"id\":\"101\",\"type\":\"USER\",\"name\":\"更早的解封人\",\"code\":\"ExistingUser\"}}";
        String wrongSnapshot =
                "{\"_actor\":{\"id\":\"999\",\"type\":\"USER\",\"name\":\"错误身份\",\"code\":null},\"password\":\"must-not-expose\"}";
        jdbc.update(
                "INSERT INTO sys_operation_log(id,actor_id,target_type,target_id,action,result,changes,created_at)"
                        + " VALUES(11,101,'IP_BLOCK',303,'IP_BLOCK_RELEASE','SUCCESS',?,'2026-01-01 00:01:00'),"
                        + "(12,101,'IP_BLOCK',303,'IP_BLOCK_RELEASE','SUCCESS',?,'2026-01-01 00:01:00'),"
                        + "(13,101,'IP_BLOCK',303,'IP_BLOCK_RELEASE','FAILURE',?,'2026-01-01 00:02:00'),"
                        + "(14,101,'USER',303,'IP_BLOCK_RELEASE','SUCCESS',?,'2026-01-01 00:03:00')",
                oldSnapshot,
                wrongSnapshot,
                oldSnapshot,
                oldSnapshot);
        String body =
                mvc.perform(get("/audit/ip-blocks/page").session(admin).param("status", "RELEASED"))
                        .andExpect(status().isOk())
                        .andExpect(jsonPath("$.data.total").value(3))
                        .andExpect(jsonPath("$.data.items[0].id").value("303"))
                        .andExpect(
                                jsonPath("$.data.items[0].unblockedByReference.name")
                                        .value("当前解封人"))
                        .andExpect(
                                jsonPath("$.data.items[0].unblockedByReference.source")
                                        .value("CURRENT"))
                        .andExpect(jsonPath("$.data.items[1].id").value("302"))
                        .andExpect(jsonPath("$.data.items[1].unblockedByReference.id").value("102"))
                        .andExpect(
                                jsonPath("$.data.items[1].unblockedByReference.source")
                                        .value("MISSING"))
                        .andExpect(jsonPath("$.data.items[2].id").value("301"))
                        .andExpect(
                                jsonPath("$.data.items[2].unblockedByReference.name")
                                        .value("当前解封人"))
                        .andExpect(
                                jsonPath("$.data.items[2].unblockedByReference.source")
                                        .value("CURRENT"))
                        .andReturn()
                        .getResponse()
                        .getContentAsString();
        assertThat(body).doesNotContain("更早的解封人", "错误身份", "must-not-expose", "changes");
    }

    @Test
    void resourceRateLimitPrecedesAnonymousSessionCreationAndDoesNotPermanentlyBlock()
            throws Exception {
        when(store.resource("192.0.2.30")).thenReturn(new LoginGuardStore.Counter(121, 53));
        for (var request :
                java.util.List.of(
                        get("/auth/login/challenge"),
                        head("/auth/login/challenge"),
                        get("/auth/csrf"))) {
            var result =
                    mvc.perform(request.with(ip("192.0.2.30")))
                            .andExpect(status().isTooManyRequests())
                            .andExpect(header().string("Retry-After", "53"))
                            .andReturn();
            assertThat(result.getRequest().getSession(false)).isNull();
        }
        assertThat(count("SELECT COUNT(*) FROM sys_ip_block")).isZero();
        verify(store, never()).failure(eq("192.0.2.30"), anyLong());
    }

    @Test
    void databaseOrCounterFailureFailsClosedWithoutPrivateDiagnostics() throws Exception {
        doThrow(new DataAccessResourceFailureException("private datasource detail"))
                .when(mapper)
                .find("192.0.2.40");
        mvc.perform(get("/auth/me").session(admin).with(ip("192.0.2.40")))
                .andExpect(status().isServiceUnavailable())
                .andExpect(jsonPath("$.code").value("DEPENDENCY_UNAVAILABLE"));
        when(store.resource("192.0.2.41"))
                .thenThrow(new DataAccessResourceFailureException("private redis detail"));
        mvc.perform(get("/auth/login/challenge").with(ip("192.0.2.41")))
                .andExpect(status().isServiceUnavailable())
                .andExpect(jsonPath("$.code").value("DEPENDENCY_UNAVAILABLE"));
    }

    @Test
    void passwordPolicyIsAuthenticatedContainsOnlyRulesAndAllowsForcedChangeAccount()
            throws Exception {
        mvc.perform(get("/auth/password-policy")).andExpect(status().isUnauthorized());
        jdbc.update("UPDATE sys_user SET must_change_password=TRUE WHERE id=?", adminId);
        var result =
                mvc.perform(get("/auth/password-policy").session(admin))
                        .andExpect(status().isOk())
                        .andExpect(jsonPath("$.data.minLength").value(8))
                        .andExpect(jsonPath("$.data.maxLength").value(64))
                        .andExpect(jsonPath("$.data.requireUppercase").value(true))
                        .andReturn();
        JsonNode rules = json.readTree(result.getResponse().getContentAsString()).path("data");
        assertThat(rules.size()).isEqualTo(6);
        assertThat(rules.has("initialPassword")).isFalse();
    }

    private int count(String sql) {
        return jdbc.queryForObject(sql, Integer.class);
    }

    @Test
    void encodedAuthenticationPathsRemainDeniedBeforeChallengeOrSessionCreation() throws Exception {
        for (String path :
                java.util.List.of(
                        "/auth/login/%63hallenge", "/auth/%63srf", "/%61uth/login/challenge")) {
            var result =
                    mvc.perform(get(java.net.URI.create(path)).with(ip("192.0.2.50")))
                            .andExpect(status().is4xxClientError())
                            .andReturn();
            assertThat(result.getRequest().getSession(false)).isNull();
        }
        assertThat(count("SELECT COUNT(*) FROM sys_ip_block")).isZero();
    }

    @Test
    void invalidResourcePathsAndMethodsCannotCreateSessionsBeforeRejection() throws Exception {
        when(store.resource("192.0.2.51")).thenReturn(new LoginGuardStore.Counter(121, 53));
        for (var request :
                java.util.List.of(
                        post(java.net.URI.create("/auth/login/%73ecure")),
                        put("/auth/login/secure"),
                        post("/auth/login/challenge"),
                        post("/auth/csrf"))) {
            var result =
                    mvc.perform(request.with(ip("192.0.2.51")))
                            .andExpect(status().is4xxClientError())
                            .andReturn();
            assertThat(result.getRequest().getSession(false)).isNull();
        }
    }

    @Test
    void concurrentThresholdTransitionsPersistOneBlockAndOneAudit() throws Exception {
        try (var workers = java.util.concurrent.Executors.newFixedThreadPool(4)) {
            var jobs = new java.util.ArrayList<java.util.concurrent.Callable<Void>>();
            for (int i = 0; i < 12; i++)
                jobs.add(
                        () -> {
                            blocks.block(
                                    "192.0.2.60", 0, 5, new AuditContextDto("192.0.2.60", null));
                            return null;
                        });
            for (var completed : workers.invokeAll(jobs)) completed.get();
        }
        assertThat(count("SELECT COUNT(*) FROM sys_ip_block WHERE source_ip='192.0.2.60'"))
                .isEqualTo(1);
        assertThat(count("SELECT COUNT(*) FROM sys_operation_log WHERE action='IP_BLOCK_CREATE'"))
                .isEqualTo(1);
    }

    private static RequestPostProcessor ip(String address) {
        return request -> {
            request.setRemoteAddr(address);
            return request;
        };
    }
}
