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
            "spring.datasource.url=jdbc:h2:mem:menu_integration;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class MenuIntegrationTest {
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
    void createsFiltersUpdatesAndDeletesARealTreeWithOneCommonResponse() throws Exception {
        Csrf auth = admin();
        JsonNode root =
                create(
                        auth,
                        """
                        {"name":"测试管理","type":"DIRECTORY","icon":"settings", "sortOrder":0,"visible":true,"enabled":true}
                        """);
        String rootId = root.path("id").asText();
        assertThat(root.has("page")).isFalse();
        assertThat(root.path("children").size()).isZero();

        JsonNode page =
                create(
                        auth,
                        """
                        {"parentId":"%s","name":"测试用户页","type":"PAGE","routeName":"TestUsers","path":"/test/users","componentKey":"TEST_USERS","moduleKey":"user","sortOrder":1,"visible":true,"enabled":true}
                        """
                                .formatted(rootId));
        String pageId = page.path("id").asText();
        assertThat(page.has("children")).isFalse();
        assertThat(page.path("page").path("moduleKey").asText()).isEqualTo("user");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE menu_id=?",
                                Integer.class,
                                Long.parseLong(pageId)))
                .isEqualTo(1);
        mvc.perform(get("/auth/me").session(auth.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.routes[1].children[0].path").value("/test/users"))
                .andExpect(jsonPath("$.data.routes[1].children[0].version").doesNotExist())
                .andExpect(jsonPath("$.data.routes[1].children[0].moduleKey").doesNotExist());

        mvc.perform(get("/menus").session(auth.session()).param("name", "用户"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.code").value("SUCCESS"))
                .andExpect(jsonPath("$.data[1].name").value("测试管理"))
                .andExpect(jsonPath("$.data[1].children[0].page.path").value("/test/users"));
        mvc.perform(get("/menus").session(auth.session()).param("type", "PAGE"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[1].children.length()").value(1));

        mvc.perform(
                        put("/menus/" + pageId)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .contentType("application/json")
                                .content(
                                        """
                                        {"parentId":"%s","name":"测试用户账号","type":"PAGE","routeName":"TestUsers","path":"/test/users","componentKey":"TEST_USERS","moduleKey":"user","sortOrder":2,"visible":false,"enabled":true,"version":"0"}
                                        """
                                                .formatted(rootId)))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.version").value("1"))
                .andExpect(jsonPath("$.data.visible").value(false));
        mvc.perform(get("/auth/me").session(auth.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.routes[1].children[0].visible").value(false));
        mvc.perform(
                        put("/menus/" + pageId)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .contentType("application/json")
                                .content(
                                        """
                                        {"parentId":"%s","name":"过期编辑","type":"PAGE","routeName":"TestUsers","path":"/test/users","componentKey":"TEST_USERS","moduleKey":"user","sortOrder":2,"visible":true,"enabled":true,"version":"0"}
                                        """
                                                .formatted(rootId)))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("VERSION_CONFLICT"));
        mvc.perform(
                        delete("/menus/" + rootId)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict());
        mvc.perform(
                        delete("/menus/" + pageId)
                                .session(auth.session())
                                .header(auth.header(), auth.token()))
                .andExpect(status().isPreconditionRequired());
        mvc.perform(
                        delete("/menus/" + pageId)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isPreconditionFailed());
        mvc.perform(
                        delete("/menus/" + pageId)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"1\""))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data").isEmpty());
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_role_menu", Integer.class))
                .isEqualTo(4);
        mvc.perform(get("/auth/me").session(auth.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.routes.length()").value(1));
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE target_type='MENU' AND result='SUCCESS'",
                                Integer.class))
                .isEqualTo(4);
    }

    @Test
    void validatesPageShapeAndRequiresMenuModuleAtRequestBoundary() throws Exception {
        Csrf auth = admin();
        mvc.perform(
                        post("/menus")
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .contentType("application/json")
                                .content(
                                        """
                                        {"name":"坏页面","type":"PAGE","routeName":"BadPage","path":"https://outside.test","componentKey":"BAD_PAGE","moduleKey":"user","sortOrder":0,"visible":true,"enabled":true}
                                        """))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
        mvc.perform(
                        post("/menus")
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .contentType("application/json")
                                .content(
                                        """
                                        {"name":"未知模块","type":"PAGE","routeName":"UnknownPage","path":"/unknown","componentKey":"UNKNOWN","moduleKey":"unknown","sortOrder":0,"visible":true,"enabled":true}
                                        """))
                .andExpect(status().isBadRequest());
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_menu", Integer.class))
                .isEqualTo(5);

        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=1 AND menu_id=1004");
        mvc.perform(get("/menus").session(auth.session()))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"));
        mvc.perform(get("/user/page").session(auth.session())).andExpect(status().isOk());
    }

    @Test
    void rejectsCyclesAndExcessDepthAndSuppressesRoutesBelowDisabledAncestors() throws Exception {
        Csrf auth = admin();
        String rootId =
                create(
                                auth,
                                """
                                {"name":"根目录","type":"DIRECTORY","sortOrder":0,"visible":true,"enabled":true}
                                """)
                        .path("id")
                        .asText();
        String childId =
                create(
                                auth,
                                """
                                {"parentId":"%s","name":"子目录","type":"DIRECTORY","sortOrder":0,"visible":true,"enabled":true}
                                """
                                        .formatted(rootId))
                        .path("id")
                        .asText();
        create(
                auth,
                """
                {"parentId":"%s","name":"用户页","type":"PAGE","routeName":"UserPage","path":"/test/users","componentKey":"TEST_USERS","moduleKey":"user","sortOrder":0,"visible":true,"enabled":true}
                """
                        .formatted(childId));
        mvc.perform(
                        put("/menus/" + rootId)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .contentType("application/json")
                                .content(
                                        """
                                        {"parentId":"%s","name":"根目录","type":"DIRECTORY","sortOrder":0,"visible":true,"enabled":true,"version":"0"}
                                        """
                                                .formatted(childId)))
                .andExpect(status().isConflict());
        mvc.perform(
                        put("/menus/" + rootId)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .contentType("application/json")
                                .content(
                                        """
                                        {"name":"根目录","type":"DIRECTORY","sortOrder":0,"visible":true,"enabled":false,"version":"0"}
                                        """))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.children.length()").value(0));
        mvc.perform(get("/auth/me").session(auth.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.routes.length()").value(1));
        mvc.perform(get("/menus").session(auth.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data[1].enabled").value(false));

        String parent = childId;
        for (int level = 3; level <= 5; level++) {
            parent =
                    create(
                                    auth,
                                    """
                                    {"parentId":"%s","name":"第%d层","type":"DIRECTORY","sortOrder":0,"visible":true,"enabled":true}
                                    """
                                            .formatted(parent, level))
                            .path("id")
                            .asText();
        }
        mvc.perform(
                        post("/menus")
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .contentType("application/json")
                                .content(
                                        """
                                        {"parentId":"%s","name":"超深页面","type":"PAGE","routeName":"TooDeep","path":"/too-deep","componentKey":"TOO_DEEP","moduleKey":"user","sortOrder":0,"visible":true,"enabled":true}
                                        """
                                                .formatted(parent)))
                .andExpect(status().isConflict());
    }

    @Test
    void ordinaryMenuEditorCreatesPageForSuperAdministratorOnly() throws Exception {
        Csrf admin = admin();
        String roleId =
                write(post("/roles"), admin, "{\"code\":\"MENU_EDITOR\",\"name\":\"菜单维护员\"}", 201)
                        .path("id")
                        .asText();
        write(
                put("/roles/" + roleId + "/menus"),
                admin,
                "{\"version\":\"0\",\"menuIds\":[\"1004\"]}",
                200);
        String userId =
                write(post("/user"), admin, "{\"username\":\"MenuEditor01\"}", 201)
                        .path("id")
                        .asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.parseLong(userId));
        write(
                put("/user/" + userId + "/roles"),
                admin,
                "{\"version\":\"0\",\"roleIds\":[\"" + roleId + "\"]}",
                200);
        Csrf editor = login("MenuEditor01", "Initial1!");
        mvc.perform(get("/user/page").session(editor.session())).andExpect(status().isForbidden());
        String menuId =
                create(
                                editor,
                                """
                {"name":"新增用户页","type":"PAGE","routeName":"NewUserPage","path":"/new/users","componentKey":"NEW_USERS","moduleKey":"user","sortOrder":0,"visible":false,"enabled":true}
                """)
                        .path("id")
                        .asText();
        assertThat(
                        jdbc.queryForList(
                                "SELECT role_id FROM sys_role_menu WHERE menu_id=?",
                                Long.class,
                                Long.parseLong(menuId)))
                .containsExactly(1L);
        mvc.perform(get("/user/page").session(editor.session())).andExpect(status().isForbidden());
        mvc.perform(get("/auth/me").session(editor.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.routes.length()").value(1))
                .andExpect(jsonPath("$.data.routes[0].children[0].path").value("/system/menus"));
        mvc.perform(
                        delete("/menus/" + menuId)
                                .session(editor.session())
                                .header(editor.header(), editor.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_role_menu WHERE menu_id=?",
                                Integer.class,
                                Long.parseLong(menuId)))
                .isZero();
        String changes =
                jdbc.queryForObject(
                        "SELECT changes FROM sys_operation_log WHERE action='MENU_DELETE' AND target_id=?",
                        String.class,
                        Long.parseLong(menuId));
        assertThat(json.readTree(changes).path("name").asText()).isEqualTo("新增用户页");
    }

    private JsonNode write(
            org.springframework.test.web.servlet.request.MockHttpServletRequestBuilder request,
            Csrf auth,
            String body,
            int expectedStatus)
            throws Exception {
        MvcResult result =
                mvc.perform(
                                request.session(auth.session())
                                        .header(auth.header(), auth.token())
                                        .contentType("application/json")
                                        .content(body))
                        .andExpect(status().is(expectedStatus))
                        .andReturn();
        return json.readTree(result.getResponse().getContentAsString()).path("data");
    }

    private JsonNode create(Csrf auth, String body) throws Exception {
        MvcResult result =
                mvc.perform(
                                post("/menus")
                                        .session(auth.session())
                                        .header(auth.header(), auth.token())
                                        .contentType("application/json")
                                        .content(body))
                        .andExpect(status().isCreated())
                        .andExpect(jsonPath("$.code").value("SUCCESS"))
                        .andReturn();
        JsonNode data = json.readTree(result.getResponse().getContentAsString()).path("data");
        assertThat(result.getResponse().getHeader("Location"))
                .isEqualTo("/menus/" + data.path("id").asText());
        return data;
    }

    private Csrf admin() throws Exception {
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        return login("Admin01", "AdminPass1!");
    }

    private Csrf login(String username, String password) throws Exception {
        Csrf csrf = csrf(null);
        MockHttpSession session =
                (MockHttpSession)
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
