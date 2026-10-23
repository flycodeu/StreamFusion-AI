package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.csrf;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.support.SecureLoginSupport;
import com.streamfusion.platform.system.auth.service.BootstrapService;
import java.util.ArrayList;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.ApplicationContext;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.security.core.userdetails.UserDetailsService;
import org.springframework.security.test.context.support.WithMockUser;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
        properties =
                "spring.datasource.url=jdbc:h2:mem:security_configuration;MODE=MySQL;DB_CLOSE_DELAY=-1")
@AutoConfigureMockMvc
@ActiveProfiles("test")
class SecurityConfigurationTest extends SecureLoginSupport {
    private final DataSource source;
    private final BootstrapService bootstrap;
    private final MockMvc mvc;
    private final ApplicationContext context;
    private final ObjectMapper mapper;

    @Autowired
    SecurityConfigurationTest(
            MockMvc mvc,
            ApplicationContext context,
            ObjectMapper mapper,
            DataSource source,
            BootstrapService bootstrap) {
        this.mvc = mvc;
        this.context = context;
        this.mapper = mapper;
        this.source = source;
        this.bootstrap = bootstrap;
    }

    @BeforeEach
    void initializeAccounts() throws Exception {
        IdentitySchema.initialize(source);
        bootstrap.initialize("DocsAdmin", null, "DocsAdmin1!", null);
    }

    private MockHttpSession authorizedSession() throws Exception {
        var result =
                mvc.perform(
                                loginRequest(mvc, mapper, null, "DocsAdmin", "DocsAdmin1!")
                                        .with(csrf()))
                        .andExpect(status().isOk())
                        .andReturn();
        return (MockHttpSession) result.getRequest().getSession(false);
    }

    @Test
    void publishesDocumentedManagementEndpoints() throws Exception {
        var response =
                mvc.perform(get("/v3/api-docs").session(authorizedSession()))
                        .andExpect(status().isOk())
                        .andReturn();
        var document = mapper.readTree(response.getResponse().getContentAsString());
        assertThat(
                        document.path("paths")
                                .path("/departments")
                                .path("post")
                                .path("summary")
                                .asText())
                .isEqualTo("创建部门");
        assertThat(
                        document.path("paths")
                                .path("/roles/{id}/menus")
                                .path("put")
                                .path("summary")
                                .asText())
                .isEqualTo("设置角色菜单");
        assertThat(document.path("paths").has("/error")).isFalse();
        var fields =
                document.path("components")
                        .path("schemas")
                        .path("DepartmentWriteDto")
                        .path("properties");
        assertThat(fields.path("parentId").path("description").asText()).isEqualTo("上级部门ID");
        assertThat(fields.has("parentIdProvided")).isFalse();
        var missingDescriptions = new ArrayList<String>();
        document.path("components")
                .path("schemas")
                .fields()
                .forEachRemaining(
                        schema -> {
                            if (!schema.getKey().endsWith("Dto") && !schema.getKey().endsWith("Vo"))
                                return;
                            schema.getValue()
                                    .path("properties")
                                    .fields()
                                    .forEachRemaining(
                                            field -> {
                                                if (field.getValue()
                                                        .path("description")
                                                        .asText()
                                                        .isBlank()) {
                                                    missingDescriptions.add(
                                                            schema.getKey() + "." + field.getKey());
                                                }
                                            });
                        });
        assertThat(missingDescriptions).isEmpty();
    }

    @Test
    void exposesBrowsableApiDocumentation() throws Exception {
        var session = authorizedSession();
        mvc.perform(get("/swagger-ui.html").session(session))
                .andExpect(status().is3xxRedirection())
                .andExpect(
                        header().string(
                                        "Location",
                                        org.hamcrest.Matchers.containsString(
                                                "/swagger-ui/index.html")));
        mvc.perform(get("/swagger-ui/index.html").session(session)).andExpect(status().isOk());
    }

    @Test
    void exposesHealthButDoesNotCreateDefaultCredentials() throws Exception {
        mvc.perform(get("/actuator/health"))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.status").value("UP"));
        assertThat(context.getBeansOfType(UserDetailsService.class)).isEmpty();
    }

    @Test
    void anonymousRequestsReceiveTracedJsonInsteadOfRedirectOrBasicChallenge() throws Exception {
        String trace = "b".repeat(32);
        for (String path : new String[] {"/user", "/login", "/private", "/actuator/env"}) {
            mvc.perform(get(path).header("X-Trace-Id", trace))
                    .andExpect(status().isUnauthorized())
                    .andExpect(header().doesNotExist("Location"))
                    .andExpect(header().doesNotExist("WWW-Authenticate"))
                    .andExpect(header().string("X-Trace-Id", trace))
                    .andExpect(jsonPath("$.code").value("UNAUTHORIZED"))
                    .andExpect(jsonPath("$.traceId").value(trace));
        }
    }

    @Test
    void rejectsUnsafeRequestsWithoutCsrf() throws Exception {
        mvc.perform(post("/user"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("CSRF_INVALID"))
                .andExpect(jsonPath("$.msg").exists())
                .andExpect(jsonPath("$.message").doesNotExist());
        mvc.perform(post("/user").with(csrf())).andExpect(status().isUnauthorized());
        mvc.perform(post("/private"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"))
                .andExpect(jsonPath("$.message").exists())
                .andExpect(jsonPath("$.msg").doesNotExist());
    }

    @Test
    @WithMockUser
    void unsupportedAuthenticationCannotEnterUserManagement() throws Exception {
        mvc.perform(get("/user"))
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.code").value("UNAUTHORIZED"));
    }
}
