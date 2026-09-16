package com.streamfusion.platform;

import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(properties = "springdoc.api-docs.enabled=false")
@AutoConfigureMockMvc
@ActiveProfiles("test")
class DisabledApiDocsSecurityTest {
    private final MockMvc mvc;

    @Autowired
    DisabledApiDocsSecurityTest(MockMvc mvc) {
        this.mvc = mvc;
    }

    @Test
    void closedDocumentationDoesNotRemainInThePublicAllowlist() throws Exception {
        mvc.perform(get("/v3/api-docs")).andExpect(status().isUnauthorized());
    }
}
