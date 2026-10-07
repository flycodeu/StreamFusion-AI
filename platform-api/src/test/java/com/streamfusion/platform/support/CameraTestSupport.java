package com.streamfusion.platform.support;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.request.MockHttpServletRequestBuilder;

/** Public HTTP helpers. Only the inherited CAPTCHA/guard store is mocked; assets use real SQL. */
@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:camera_integration;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.initial-password=Initial1!",
            "platform.camera.active-key-id=testkey",
            "platform.camera.keys.testkey=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
            "platform.camera.network-policies.test.name=Isolated test destinations",
            "platform.camera.network-policies.test.cidrs[0]=10.0.0.0/8"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
public abstract class CameraTestSupport extends SecureLoginSupport {
    @Autowired protected MockMvc mvc;
    @Autowired protected ObjectMapper json;
    @Autowired protected DataSource dataSource;
    @Autowired protected BootstrapService bootstrap;
    protected JdbcTemplate jdbc;

    @BeforeEach
    void resetCameraDatabase() throws Exception {
        IdentitySchema.initialize(dataSource);
        jdbc = new JdbcTemplate(dataSource);
    }

    protected Csrf admin() throws Exception {
        bootstrap.initialize("CameraAdmin", null, "AdminPass1!", null);
        return login("CameraAdmin", "AdminPass1!");
    }

    protected Csrf login(String username, String password) throws Exception {
        Csrf initial = csrf(null);
        MockHttpSession session =
                (MockHttpSession)
                        mvc.perform(
                                        loginRequest(
                                                        mvc,
                                                        json,
                                                        initial.session(),
                                                        username,
                                                        password)
                                                .header(initial.header(), initial.token()))
                                .andExpect(status().isOk())
                                .andReturn()
                                .getRequest()
                                .getSession(false);
        return csrf(session);
    }

    protected Csrf csrf(MockHttpSession session) throws Exception {
        var request = get("/auth/csrf");
        if (session != null) request.session(session);
        var result = mvc.perform(request).andExpect(status().isOk()).andReturn();
        JsonNode data = json.readTree(result.getResponse().getContentAsByteArray()).path("data");
        return new Csrf(
                (MockHttpSession) result.getRequest().getSession(false),
                data.path("headerName").asText(),
                data.path("token").asText());
    }

    protected JsonNode write(
            MockHttpServletRequestBuilder request, Csrf auth, Object body, int expected)
            throws Exception {
        var result =
                mvc.perform(
                                request.session(auth.session())
                                        .header(auth.header(), auth.token())
                                        .contentType("application/json")
                                        .content(json.writeValueAsBytes(body)))
                        .andExpect(status().is(expected))
                        .andReturn();
        JsonNode response = json.readTree(result.getResponse().getContentAsByteArray());
        if (expected < 300)
            org.assertj.core.api.Assertions.assertThat(response.path("code").asText())
                    .isEqualTo("SUCCESS");
        return response.path("data");
    }

    protected JsonNode read(String path, Csrf auth) throws Exception {
        var result =
                mvc.perform(get(path).session(auth.session()))
                        .andExpect(status().isOk())
                        .andExpect(jsonPath("$.code").value("SUCCESS"))
                        .andReturn();
        return json.readTree(result.getResponse().getContentAsByteArray()).path("data");
    }

    protected JsonNode source(Csrf auth) throws Exception {
        return write(post("/camera-sources"), auth, sourceBody(requestId()), 201);
    }

    protected Map<String, Object> sourceBody(String requestId) {
        return Map.of(
                "clientRequestId",
                requestId,
                "name",
                "Test RTSP source",
                "adapterType",
                "RTSP",
                "networkPolicyKey",
                "test",
                "enabled",
                true,
                "credentials",
                List.of(),
                "endpoints",
                List.of(
                        Map.of(
                                "purpose",
                                "RTSP",
                                "scheme",
                                "rtsp",
                                "host",
                                "10.0.1.5",
                                "port",
                                554,
                                "basePath",
                                "",
                                "authMode",
                                "NONE",
                                "tlsPolicy",
                                "SYSTEM_CA")));
    }

    protected JsonNode camera(Csrf auth, String sourceId, String name) throws Exception {
        return write(post("/cameras"), auth, cameraBody(sourceId, name, requestId()), 201);
    }

    protected Map<String, Object> cameraBody(String sourceId, String name, String requestId) {
        return Map.of(
                "sourceId",
                sourceId,
                "sourceVersion",
                "0",
                "clientRequestId",
                requestId,
                "name",
                name,
                "defaultProfileClientKey",
                "main",
                "profiles",
                List.of(
                        profileBody("main", "MAIN", "/private-main"),
                        profileBody("sub", "SUB", "/private-sub")));
    }

    protected Map<String, Object> profileBody(String clientKey, String usage, String path) {
        return Map.of(
                "clientKey",
                clientKey,
                "label",
                clientKey,
                "usageHint",
                usage,
                "enabled",
                true,
                "locatorKind",
                "RTSP",
                "locator",
                locatorBody(path));
    }

    protected Map<String, Object> locatorBody(String path) {
        return Map.of(
                "hostMode",
                "SOURCE",
                "transport",
                "TCP",
                "pathSecret",
                Map.of("action", "REPLACE", "value", path),
                "querySecret",
                Map.of("action", "CLEAR"));
    }

    protected static String requestId() {
        return System.currentTimeMillis() + "-" + UUID.randomUUID();
    }

    protected record Csrf(MockHttpSession session, String header, String token) {}
}
