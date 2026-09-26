package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.delete;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.support.IdentitySchema;
import java.util.Map;
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
import org.springframework.test.web.servlet.MvcResult;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:department_integration;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class DepartmentIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    private JdbcTemplate jdbc;

    @BeforeEach
    void reset() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        // These cases exercise a user-created tree; seed organization behavior has its own tests.
        jdbc.update("DELETE FROM sys_dept WHERE id IN (2002,2003)");
        jdbc.update("DELETE FROM sys_dept WHERE id=2001");
    }

    @Test
    void managesMultipleRootsMovesSubtreesAndAssignsSeveralDepartments() throws Exception {
        Csrf admin = admin();
        String companyA =
                write(
                                post("/departments"),
                                admin,
                                "{\"parentId\":null,\"name\":\"甲公司\",\"sortOrder\":0}",
                                201)
                        .path("id")
                        .asText();
        String companyB =
                write(
                                post("/departments"),
                                admin,
                                "{\"parentId\":null,\"name\":\"乙公司\",\"sortOrder\":1}",
                                201)
                        .path("id")
                        .asText();
        mvc.perform(
                        post("/departments")
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content("{\"parentId\":null,\"name\":\"甲公司\",\"sortOrder\":2}"))
                .andExpect(status().isConflict());
        String deptA =
                write(
                                post("/departments"),
                                admin,
                                "{\"parentId\":\""
                                        + companyA
                                        + "\",\"name\":\"研发\",\"sortOrder\":0}",
                                201)
                        .path("id")
                        .asText();
        String deptB =
                write(
                                post("/departments"),
                                admin,
                                "{\"parentId\":\""
                                        + companyB
                                        + "\",\"name\":\"运维\",\"sortOrder\":0}",
                                201)
                        .path("id")
                        .asText();
        mvc.perform(get("/departments").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.length()").value(2))
                .andExpect(jsonPath("$.data[0].children.length()").value(1));
        write(
                put("/departments/" + deptB),
                admin,
                "{\"version\":\"0\",\"parentId\":\""
                        + companyA
                        + "\",\"name\":\"运维\",\"sortOrder\":1}",
                200);
        mvc.perform(
                        put("/departments/" + deptB)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content(
                                        "{\"version\":\"0\",\"parentId\":\""
                                                + companyB
                                                + "\",\"name\":\"运维\",\"sortOrder\":0}"))
                .andExpect(status().isConflict());
        mvc.perform(
                        put("/departments/" + deptB)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content("{\"version\":\"1\",\"name\":\"运维\",\"sortOrder\":1}"))
                .andExpect(status().isBadRequest());
        mvc.perform(
                        put("/departments/" + companyA)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content(
                                        "{\"version\":\"0\",\"parentId\":\""
                                                + deptA
                                                + "\",\"name\":\"甲公司\",\"sortOrder\":0}"))
                .andExpect(status().isConflict());
        mvc.perform(
                        delete("/departments/" + companyA)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict());

        String userId =
                write(post("/user"), admin, "{\"username\":\"Worker01\"}", 201).path("id").asText();
        write(
                put("/user/" + userId + "/departments"),
                admin,
                "{\"version\":\"0\",\"departmentIds\":[\"" + deptA + "\",\"" + deptB + "\"]}",
                200);
        JsonNode assigned = auditChanges("USER_DEPARTMENTS_UPDATE", userId);
        assertThat(assigned.path("beforeDepartmentIds").isArray()).isTrue();
        assertThat(assigned.path("beforeDepartmentIds").size()).isZero();
        assertThat(assigned.path("afterDepartmentIds").size()).isEqualTo(2);
        assertThat(assigned.path("afterDepartmentIds").get(0).asText()).isEqualTo(deptA);
        mvc.perform(get("/user/" + userId).session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.departments.length()").value(2));
        mvc.perform(get("/departments").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[0].children[0].memberCount").value(1))
                .andExpect(jsonPath("$.data[0].children[1].memberCount").value(1));
        mvc.perform(
                        delete("/departments/" + deptA)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict());
        write(
                put("/user/" + userId + "/departments"),
                admin,
                "{\"version\":\"1\",\"departmentIds\":[]}",
                200);
        mvc.perform(
                        delete("/departments/" + deptA)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        JsonNode deleted = auditChanges("DEPT_DELETE", deptA);
        assertThat(deleted.path("name").asText()).isEqualTo("研发");
        assertThat(deleted.path("parentId").asText()).isEqualTo(companyA);
        write(
                put("/user/" + userId + "/departments"),
                admin,
                "{\"version\":\"2\",\"departmentIds\":[\"" + deptB + "\"]}",
                200);
        mvc.perform(
                        delete("/user/" + userId)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"3\""))
                .andExpect(status().isOk());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user_dept WHERE user_id=?",
                                Integer.class,
                                Long.parseLong(userId)))
                .isZero();
    }

    @Test
    void userModuleCanReadMinimalOptionsWithoutDepartmentManagement() throws Exception {
        Csrf admin = admin();
        write(
                post("/departments"),
                admin,
                "{\"parentId\":null,\"name\":\"甲公司\",\"sortOrder\":0}",
                201);
        String roleId =
                write(post("/roles"), admin, "{\"code\":\"USER_ONLY\",\"name\":\"用户维护\"}", 201)
                        .path("id")
                        .asText();
        write(
                put("/roles/" + roleId + "/menus"),
                admin,
                "{\"version\":\"0\",\"menuIds\":[\"1002\"]}",
                200);
        String userId =
                write(post("/user"), admin, "{\"username\":\"Worker01\"}", 201).path("id").asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.parseLong(userId));
        write(
                put("/user/" + userId + "/roles"),
                admin,
                "{\"version\":\"0\",\"roleIds\":[\"" + roleId + "\"]}",
                200);
        Csrf worker = login("Worker01", "Initial1!");
        mvc.perform(get("/user/department-options").session(worker.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[0].name").value("甲公司"));
        mvc.perform(get("/departments").session(worker.session()))
                .andExpect(status().isForbidden());
        long superUserId =
                jdbc.queryForObject("SELECT id FROM sys_user WHERE username='Admin01'", Long.class);
        mvc.perform(
                        put("/user/" + superUserId + "/departments")
                                .session(worker.session())
                                .header(worker.header(), worker.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"departmentIds\":[]}"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PROTECTED_ACCOUNT"));
        mvc.perform(
                        put("/user/" + userId + "/departments")
                                .session(worker.session())
                                .header(worker.header(), worker.token())
                                .contentType("application/json")
                                .content("{\"version\":\"1\",\"departmentIds\":[]}"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PROTECTED_ACCOUNT"));
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='USER_DEPARTMENTS_UPDATE'",
                                Integer.class))
                .isZero();
    }

    @Test
    void superAdministratorsCanReassignAndClearTheirOwnAndOtherSuperDepartments() throws Exception {
        Csrf admin = admin();
        String adminId =
                jdbc.queryForObject("SELECT id FROM sys_user WHERE username='Admin01'", Long.class)
                        .toString();
        String firstDepartment =
                write(
                                post("/departments"),
                                admin,
                                "{\"parentId\":null,\"name\":\"测试甲部门\",\"sortOrder\":0}",
                                201)
                        .path("id")
                        .asText();
        String secondDepartment =
                write(
                                post("/departments"),
                                admin,
                                "{\"parentId\":null,\"name\":\"测试乙部门\",\"sortOrder\":1}",
                                201)
                        .path("id")
                        .asText();
        write(
                put("/user/" + adminId + "/departments"),
                admin,
                "{\"version\":\"0\",\"departmentIds\":[\"" + firstDepartment + "\"]}",
                200);
        JsonNode another = write(post("/user"), admin, "{\"username\":\"AnotherAdmin\"}", 201);
        String anotherId = another.path("id").asText();
        assertThat(another.path("departments").size()).isZero();
        write(
                put("/user/" + anotherId + "/roles"),
                admin,
                "{\"version\":\"0\",\"roleIds\":[\"1\"]}",
                200);
        write(
                put("/user/" + anotherId + "/departments"),
                admin,
                "{\"version\":\"1\",\"departmentIds\":[\"" + firstDepartment + "\"]}",
                200);
        mvc.perform(
                        delete("/departments/" + firstDepartment)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict());
        write(
                put("/user/" + adminId + "/departments"),
                admin,
                "{\"version\":\"1\",\"departmentIds\":[\"" + secondDepartment + "\"]}",
                200);
        mvc.perform(get("/user/" + adminId + "/departments").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.departments[0].id").value(secondDepartment));
        write(
                put("/user/" + anotherId + "/departments"),
                admin,
                "{\"version\":\"2\",\"departmentIds\":[]}",
                200);
        mvc.perform(
                        delete("/departments/" + firstDepartment)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        write(
                put("/user/" + adminId + "/departments"),
                admin,
                "{\"version\":\"2\",\"departmentIds\":[]}",
                200);
        mvc.perform(
                        delete("/departments/" + secondDepartment)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        mvc.perform(get("/auth/me").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.isSuperAdmin").value(true));
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user_role WHERE role_id=1",
                                Integer.class))
                .isEqualTo(2);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_user_dept", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_dept", Integer.class)).isZero();
    }

    @Test
    void rejectsAChildBeyondTheEighthLevel() throws Exception {
        Csrf admin = admin();
        String parent = null;
        for (int level = 1; level <= 8; level++) {
            String parentValue = parent == null ? "null" : "\"" + parent + "\"";
            parent =
                    write(
                                    post("/departments"),
                                    admin,
                                    "{\"parentId\":"
                                            + parentValue
                                            + ",\"name\":\"层"
                                            + level
                                            + "\",\"sortOrder\":0}",
                                    201)
                            .path("id")
                            .asText();
        }
        mvc.perform(
                        post("/departments")
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content(
                                        "{\"parentId\":\""
                                                + parent
                                                + "\",\"name\":\"层9\",\"sortOrder\":0}"))
                .andExpect(status().isConflict());
    }

    private JsonNode write(
            org.springframework.test.web.servlet.request.MockHttpServletRequestBuilder request,
            Csrf auth,
            String body,
            int status)
            throws Exception {
        MvcResult result =
                mvc.perform(
                                request.session(auth.session())
                                        .header(auth.header(), auth.token())
                                        .contentType("application/json")
                                        .content(body))
                        .andExpect(
                                org.springframework.test.web.servlet.result.MockMvcResultMatchers
                                        .status()
                                        .is(status))
                        .andExpect(jsonPath("$.code").value("SUCCESS"))
                        .andReturn();
        return json.readTree(result.getResponse().getContentAsString()).path("data");
    }

    private JsonNode auditChanges(String action, String targetId) throws Exception {
        String stored =
                jdbc.queryForObject(
                        "SELECT changes FROM sys_operation_log WHERE action=? AND target_id=? ORDER BY id DESC LIMIT 1",
                        String.class,
                        action,
                        Long.parseLong(targetId));
        return json.readTree(stored);
    }

    private Csrf admin() throws Exception {
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        return login("Admin01", "AdminPass1!");
    }

    private Csrf login(String username, String password) throws Exception {
        Csrf initial = csrf(null);
        MockHttpSession session =
                (MockHttpSession)
                        mvc.perform(
                                        post("/auth/login")
                                                .session(initial.session())
                                                .header(initial.header(), initial.token())
                                                .contentType("application/json")
                                                .content(
                                                        json.writeValueAsString(
                                                                Map.of(
                                                                        "username",
                                                                        username,
                                                                        "password",
                                                                        password))))
                                .andExpect(status().isOk())
                                .andReturn()
                                .getRequest()
                                .getSession(false);
        return csrf(session);
    }

    private Csrf csrf(MockHttpSession session) throws Exception {
        var request = get("/auth/csrf");
        if (session != null) request.session(session);
        MvcResult result = mvc.perform(request).andExpect(status().isOk()).andReturn();
        JsonNode data = json.readTree(result.getResponse().getContentAsString()).path("data");
        return new Csrf(
                (MockHttpSession) result.getRequest().getSession(false),
                data.path("headerName").asText(),
                data.path("token").asText());
    }

    private record Csrf(MockHttpSession session, String header, String token) {}
}
