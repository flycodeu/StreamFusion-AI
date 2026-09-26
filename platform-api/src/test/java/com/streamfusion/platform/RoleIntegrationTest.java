package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.hamcrest.Matchers.hasSize;
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
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
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
            "spring.datasource.url=jdbc:h2:mem:role_integration;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class RoleIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    private JdbcTemplate jdbc;

    @BeforeEach
    void reset() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
    }

    @Test
    void directorySelectionStoresPagesAndUserReceivesOnlyAssignedRoutes() throws Exception {
        Csrf admin = admin();
        JsonNode role =
                write(
                        post("/roles"),
                        admin,
                        """
                {"code":"OPS","name":"操作员","description":"测试角色"}
                """,
                        201);
        String roleId = role.path("id").asText();
        mvc.perform(get("/roles/" + roleId + "/menus").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.selectedPageIds.length()").value(0))
                .andExpect(jsonPath("$.data.tree[0].children.length()").value(4));
        write(
                put("/roles/" + roleId + "/menus"),
                admin,
                "{\"version\":\"0\",\"menuIds\":[\"1001\"]}",
                200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=?",
                                Integer.class,
                                Long.parseLong(roleId)))
                .isEqualTo(4);
        write(
                put("/roles/" + roleId + "/menus"),
                admin,
                "{\"version\":\"1\",\"menuIds\":[\"1002\"]}",
                200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=?",
                                Integer.class,
                                Long.parseLong(roleId)))
                .isEqualTo(1);

        JsonNode worker = write(post("/user"), admin, "{\"username\":\"Worker01\"}", 201);
        String userId = worker.path("id").asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.parseLong(userId));
        write(
                put("/user/" + userId + "/roles"),
                admin,
                "{\"version\":\"0\",\"roleIds\":[\"" + roleId + "\"]}",
                200);
        mvc.perform(get("/user/" + userId + "/menus").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[0].children.length()").value(1));
        Csrf ordinary = login("Worker01", "Initial1!");
        mvc.perform(get("/auth/me").session(ordinary.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.roles[0].code").value("OPS"))
                .andExpect(jsonPath("$.data.modules[0]").value("user"))
                .andExpect(jsonPath("$.data.routes[0].children[0].path").value("/system/users"));
        mvc.perform(get("/user/page").session(ordinary.session())).andExpect(status().isOk());
        mvc.perform(get("/roles").session(ordinary.session())).andExpect(status().isForbidden());
        String targetId =
                write(post("/user"), admin, "{\"username\":\"Target01\"}", 201).path("id").asText();
        mvc.perform(
                        put("/user/" + targetId + "/roles")
                                .session(ordinary.session())
                                .header(ordinary.header(), ordinary.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"roleIds\":[\"1\"]}"))
                .andExpect(status().isForbidden());
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE id=1001");
        mvc.perform(get("/user/page").session(ordinary.session()))
                .andExpect(status().isForbidden());
        jdbc.update("UPDATE sys_menu SET enabled=TRUE WHERE id=1001");
        mvc.perform(get("/user/page").session(ordinary.session())).andExpect(status().isOk());

        write(put("/roles/" + roleId + "/menus"), admin, "{\"version\":\"2\",\"menuIds\":[]}", 200);
        mvc.perform(get("/user/page").session(ordinary.session()))
                .andExpect(status().isForbidden());
        mvc.perform(get("/auth/me").session(ordinary.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.routes.length()").value(0));
        mvc.perform(
                        delete("/roles/" + roleId)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"3\""))
                .andExpect(status().isConflict());
        write(put("/user/" + userId + "/roles"), admin, "{\"version\":\"1\",\"roleIds\":[]}", 200);
        mvc.perform(
                        delete("/roles/" + roleId)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"3\""))
                .andExpect(status().isOk());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role WHERE code='OPS'", Integer.class))
                .isZero();
    }

    @Test
    void pageAdministratorCanManageOrdinaryRolesBeyondOwnPages() throws Exception {
        Csrf admin = admin();
        String roleId = createRole(admin, "MANAGER", List.of("1002", "1003"));
        String userId = createUser(admin, "Worker01");
        assignRoles(admin, userId, "0", List.of(roleId));
        Csrf ordinary = login("Worker01", "Initial1!");
        mvc.perform(get("/menus").session(ordinary.session())).andExpect(status().isForbidden());

        String adminRoleId =
                write(post("/roles"), ordinary, "{\"code\":\"ADMIN\",\"name\":\"管理员\"}", 201)
                        .path("id")
                        .asText();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=?",
                                Integer.class,
                                Long.parseLong(adminRoleId)))
                .isZero();
        write(
                put("/roles/" + adminRoleId + "/menus"),
                ordinary,
                "{\"version\":\"0\",\"menuIds\":[\"1004\"]}",
                200);
        mvc.perform(get("/user/role-options").session(ordinary.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[?(@.code == 'ADMIN')]", hasSize(1)))
                .andExpect(jsonPath("$.data[?(@.code == 'SUPER_ADMIN')]").isEmpty());
        mvc.perform(get("/roles/options").session(ordinary.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[?(@.code == 'SUPER_ADMIN')]").isEmpty());
        String targetId = createUser(admin, "Target01");
        assignRoles(ordinary, targetId, "0", List.of(adminRoleId));
        mvc.perform(get("/user/" + targetId + "/menus").session(ordinary.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[0].children[0].path").value("/system/menus"));

        // A role administrator may edit their own ordinary role and select a page they lack.
        write(
                put("/roles/" + roleId + "/menus"),
                ordinary,
                "{\"version\":\"1\",\"menuIds\":[\"1002\",\"1003\",\"1004\"]}",
                200);
        mvc.perform(get("/menus").session(ordinary.session())).andExpect(status().isOk());
        write(put("/roles/" + roleId), ordinary, "{\"version\":\"2\",\"name\":\"新的管理员名称\"}", 200);
        assignRoles(ordinary, userId, "1", List.of(roleId, adminRoleId));

        mvc.perform(
                        put("/roles/1/menus")
                                .session(ordinary.session())
                                .header(ordinary.header(), ordinary.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"menuIds\":[]}"))
                .andExpect(status().isForbidden());
        mvc.perform(
                        put("/user/" + targetId + "/roles")
                                .session(ordinary.session())
                                .header(ordinary.header(), ordinary.token())
                                .contentType("application/json")
                                .content("{\"version\":\"1\",\"roleIds\":[\"1\"]}"))
                .andExpect(status().isForbidden());
        long superUserId =
                jdbc.queryForObject("SELECT id FROM sys_user WHERE username='Admin01'", Long.class);
        mvc.perform(
                        put("/user/" + superUserId + "/roles")
                                .session(ordinary.session())
                                .header(ordinary.header(), ordinary.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"roleIds\":[]}"))
                .andExpect(status().isForbidden());
    }

    @Test
    void disabledRolesCanBeRetainedOrRemovedButCannotBeNewlyAssigned() throws Exception {
        Csrf admin = admin();
        String managerRoleId = createRole(admin, "MANAGER", List.of("1002"));
        String dormantRoleId = createRole(admin, "DORMANT", List.of("1004"));
        String managerId = createUser(admin, "Manager01");
        String targetId = createUser(admin, "Target01");
        assignRoles(admin, managerId, "0", List.of(managerRoleId));
        assignRoles(admin, targetId, "0", List.of(dormantRoleId));
        write(post("/roles/" + dormantRoleId + "/disable"), admin, "{\"version\":\"1\"}", 200);
        Csrf ordinary = login("Manager01", "Initial1!");
        mvc.perform(get("/user/role-options").session(ordinary.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[?(@.code == 'DORMANT')]").isEmpty());
        mvc.perform(get("/user/" + targetId + "/roles").session(ordinary.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.roles[0].code").value("DORMANT"));
        assignRoles(ordinary, targetId, "1", List.of(dormantRoleId));
        assignRoles(ordinary, targetId, "1", List.of());
        mvc.perform(
                        put("/user/" + targetId + "/roles")
                                .session(ordinary.session())
                                .header(ordinary.header(), ordinary.token())
                                .contentType("application/json")
                                .content(
                                        json.writeValueAsString(
                                                Map.of(
                                                        "version",
                                                        "2",
                                                        "roleIds",
                                                        List.of(dormantRoleId)))))
                .andExpect(status().isBadRequest());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user_role WHERE user_id=?",
                                Integer.class,
                                Long.parseLong(targetId)))
                .isZero();
        String changes =
                jdbc.queryForObject(
                        "SELECT changes FROM sys_operation_log WHERE action='USER_ROLE_UPDATE' AND target_id=? ORDER BY created_at DESC,id DESC LIMIT 1",
                        String.class,
                        Long.parseLong(targetId));
        assertThat(json.readTree(changes).path("beforeRoleIds").get(0).asText())
                .isEqualTo(dormantRoleId);
        assertThat(json.readTree(changes).path("afterRoleIds").size()).isZero();
    }

    @ParameterizedTest
    @ValueSource(booleans = {false, true})
    void disabledPagesCanBeRetainedOrRemovedWithoutNewGrants(boolean disableAncestor)
            throws Exception {
        Csrf admin = admin();
        Map<String, Object> directoryBody =
                Map.of(
                        "parentId",
                        "1001",
                        "name",
                        "测试目录",
                        "type",
                        "DIRECTORY",
                        "sortOrder",
                        10,
                        "visible",
                        true,
                        "enabled",
                        true);
        String directoryId =
                write(post("/menus"), admin, json.writeValueAsString(directoryBody), 201)
                        .path("id")
                        .asText();
        Map<String, Object> pageBody =
                Map.of(
                        "parentId",
                        directoryId,
                        "name",
                        "测试页面",
                        "type",
                        "PAGE",
                        "routeName",
                        "DormantPage",
                        "path",
                        "/test/dormant",
                        "componentKey",
                        "SYSTEM_DEPARTMENTS",
                        "moduleKey",
                        "department",
                        "sortOrder",
                        0,
                        "visible",
                        true,
                        "enabled",
                        true);
        String pageId =
                write(post("/menus"), admin, json.writeValueAsString(pageBody), 201)
                        .path("id")
                        .asText();
        String existingRole = createRole(admin, "EXISTING", List.of(pageId));
        String newRole = createRole(admin, "NEW_ROLE", List.of());
        Map<String, Object> disabled = new HashMap<>(disableAncestor ? directoryBody : pageBody);
        disabled.put("version", "0");
        disabled.put("enabled", false);
        write(
                put("/menus/" + (disableAncestor ? directoryId : pageId)),
                admin,
                json.writeValueAsString(disabled),
                200);

        // Keep an explicit existing binding while editing unrelated pages.
        write(
                put("/roles/" + existingRole + "/menus"),
                admin,
                json.writeValueAsString(Map.of("version", "1", "menuIds", List.of(pageId, "1002"))),
                200);
        mvc.perform(get("/roles/" + existingRole + "/menus").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.selectedPageIds", hasSize(2)));
        assertThat(
                        jdbc.queryForList(
                                "SELECT menu_id FROM sys_role_menu WHERE role_id=?",
                                Long.class,
                                Long.parseLong(existingRole)))
                .containsExactlyInAnyOrder(Long.parseLong(pageId), 1002L);

        mvc.perform(
                        put("/roles/" + newRole + "/menus")
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content(
                                        json.writeValueAsString(
                                                Map.of(
                                                        "version",
                                                        "1",
                                                        "menuIds",
                                                        List.of(pageId)))))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
        // Expanding an enabled ancestor does not grant its disabled descendants.
        write(
                put("/roles/" + newRole + "/menus"),
                admin,
                "{\"version\":\"1\",\"menuIds\":[\"1001\"]}",
                200);
        assertThat(
                        jdbc.queryForList(
                                "SELECT menu_id FROM sys_role_menu WHERE role_id=?",
                                Long.class,
                                Long.parseLong(newRole)))
                .containsExactlyInAnyOrder(1002L, 1003L, 1004L, 1005L);

        write(
                put("/roles/" + existingRole + "/menus"),
                admin,
                "{\"version\":\"2\",\"menuIds\":[\"1002\"]}",
                200);
        mvc.perform(
                        put("/roles/" + existingRole + "/menus")
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content(
                                        json.writeValueAsString(
                                                Map.of(
                                                        "version",
                                                        "3",
                                                        "menuIds",
                                                        List.of(pageId, "1002")))))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
        String changes =
                jdbc.queryForObject(
                        "SELECT changes FROM sys_operation_log WHERE action='ROLE_MENU_UPDATE' AND target_id=? ORDER BY created_at DESC,id DESC LIMIT 1",
                        String.class,
                        Long.parseLong(existingRole));
        assertThat(json.readTree(changes).path("beforeMenuIds").size()).isEqualTo(2);
        assertThat(json.readTree(changes).path("afterMenuIds").get(0).asText()).isEqualTo("1002");
    }

    @Test
    void staleRoleWritesConflictAndDeleteUsesPreconditionStatus() throws Exception {
        Csrf admin = admin();
        String roleId =
                write(post("/roles"), admin, "{\"code\":\"OPS\",\"name\":\"原角色\"}", 201)
                        .path("id")
                        .asText();
        write(put("/roles/" + roleId), admin, "{\"version\":\"0\",\"name\":\"新角色\"}", 200);
        mvc.perform(
                        put("/roles/" + roleId + "/menus")
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"menuIds\":[\"1004\"]}"))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("VERSION_CONFLICT"));
        mvc.perform(
                        delete("/roles/" + roleId)
                                .session(admin.session())
                                .header(admin.header(), admin.token()))
                .andExpect(status().isPreconditionRequired());
        mvc.perform(
                        delete("/roles/" + roleId)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isPreconditionFailed())
                .andExpect(jsonPath("$.code").value("PRECONDITION_FAILED"));
        assertThat(
                        jdbc.queryForObject(
                                "SELECT name FROM sys_role WHERE id=?",
                                String.class,
                                Long.parseLong(roleId)))
                .isEqualTo("新角色");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE role_id=?",
                                Integer.class,
                                Long.parseLong(roleId)))
                .isZero();
        mvc.perform(
                        delete("/roles/" + roleId)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"1\""))
                .andExpect(status().isOk());
        String changes =
                jdbc.queryForObject(
                        "SELECT changes FROM sys_operation_log WHERE action='ROLE_DELETE' AND target_id=?",
                        String.class,
                        Long.parseLong(roleId));
        assertThat(json.readTree(changes).path("code").asText()).isEqualTo("OPS");
        assertThat(json.readTree(changes).path("name").asText()).isEqualTo("新角色");
    }

    @Test
    void superAdministratorCanGrantAndRevokeSuperRoleForAnotherUser() throws Exception {
        Csrf admin = admin();
        mvc.perform(get("/user/role-options").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[0].code").value("SUPER_ADMIN"));
        String userId =
                write(post("/user"), admin, "{\"username\":\"Worker01\"}", 201).path("id").asText();
        write(
                put("/user/" + userId + "/roles"),
                admin,
                "{\"version\":\"0\",\"roleIds\":[\"1\"]}",
                200);
        mvc.perform(get("/user/" + userId + "/roles").session(admin.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.roles[0].code").value("SUPER_ADMIN"));
        write(put("/user/" + userId + "/roles"), admin, "{\"version\":\"1\",\"roleIds\":[]}", 200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_user_role WHERE user_id=?",
                                Integer.class,
                                Long.parseLong(userId)))
                .isZero();
        long superUserId =
                jdbc.queryForObject("SELECT id FROM sys_user WHERE username='Admin01'", Long.class);
        mvc.perform(
                        put("/user/" + superUserId + "/roles")
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .contentType("application/json")
                                .content("{\"version\":\"0\",\"roleIds\":[]}"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("PROTECTED_ACCOUNT"));
    }

    private String createRole(Csrf actor, String code, List<String> menuIds) throws Exception {
        String id =
                write(
                                post("/roles"),
                                actor,
                                json.writeValueAsString(Map.of("code", code, "name", code)),
                                201)
                        .path("id")
                        .asText();
        write(
                put("/roles/" + id + "/menus"),
                actor,
                json.writeValueAsString(Map.of("version", "0", "menuIds", menuIds)),
                200);
        return id;
    }

    private String createUser(Csrf actor, String username) throws Exception {
        String id =
                write(
                                post("/user"),
                                actor,
                                json.writeValueAsString(Map.of("username", username)),
                                201)
                        .path("id")
                        .asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.parseLong(id));
        return id;
    }

    private void assignRoles(Csrf actor, String userId, String version, List<String> roleIds)
            throws Exception {
        write(
                put("/user/" + userId + "/roles"),
                actor,
                json.writeValueAsString(Map.of("version", version, "roleIds", roleIds)),
                200);
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
