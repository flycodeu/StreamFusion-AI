package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.csrf;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

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

    @Autowired
    SecurityConfigurationTest(MockMvc mvc, ApplicationContext context) {
        this.mvc = mvc;
        this.context = context;
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
        for (String path : new String[] {"/api/v1/users", "/login", "/private", "/actuator/env"}) {
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
        mvc.perform(post("/api/v1/users"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"));
        mvc.perform(post("/api/v1/users").with(csrf())).andExpect(status().isUnauthorized());
    }

    @Test
    @WithMockUser
    void authenticatedRequestsAreStillDeniedUntilBusinessRulesExist() throws Exception {
        mvc.perform(get("/api/v1/users"))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"));
    }
}
