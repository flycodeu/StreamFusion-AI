package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.csrf;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.ArrayList;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.ApplicationContext;
import org.springframework.security.core.userdetails.UserDetailsService;
import org.springframework.security.test.context.support.WithMockUser;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest
@AutoConfigureMockMvc
@ActiveProfiles("test")
class SecurityConfigurationTest {
    private final MockMvc mvc;
    private final ApplicationContext context;
    private final ObjectMapper mapper;

    @Autowired
    SecurityConfigurationTest(MockMvc mvc, ApplicationContext context, ObjectMapper mapper) {
        this.mvc = mvc;
        this.context = context;
        this.mapper = mapper;
    }

    @Test
    void publishesDocumentedManagementEndpoints() throws Exception {
        var response = mvc.perform(get("/v3/api-docs")).andExpect(status().isOk()).andReturn();
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
        mvc.perform(get("/swagger-ui.html"))
                .andExpect(status().is3xxRedirection())
                .andExpect(
                        header().string(
                                        "Location",
                                        org.hamcrest.Matchers.containsString(
                                                "/swagger-ui/index.html")));
        mvc.perform(get("/swagger-ui/index.html")).andExpect(status().isOk());
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
