package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.pojo.dto.EncryptedLoginDto;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.auth.service.LoginCipherService;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.support.SecureLoginSupport;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:encrypted_login;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.max-password-length=128"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class LoginCipherIntegrationTest extends SecureLoginSupport {
    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired LoginCipherService cipherService;
    @Autowired PasswordService passwords;

    @BeforeEach
    void reset() throws Exception {
        IdentitySchema.initialize(source);
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
    }

    @Test
    void acceptsEncryptedCredentialsAndConsumesChallengeOnce() throws Exception {
        var csrfResult = mvc.perform(get("/auth/csrf")).andExpect(status().isOk()).andReturn();
        var session = (MockHttpSession) csrfResult.getRequest().getSession(false);
        JsonNode csrf = json.readTree(csrfResult.getResponse().getContentAsString()).path("data");
        mvc.perform(
                        post("/auth/login")
                                .session(session)
                                .header(
                                        csrf.path("headerName").asText(),
                                        csrf.path("token").asText())
                                .contentType("application/json")
                                .content("{\"username\":\"Admin01\",\"password\":\"AdminPass1!\"}"))
                .andExpect(status().isUnauthorized());

        JsonNode challenge =
                json.readTree(
                                mvc.perform(get("/auth/login/challenge").session(session))
                                        .andExpect(status().isOk())
                                        .andReturn()
                                        .getResponse()
                                        .getContentAsString())
                        .path("data");
        String envelope = encrypt(challenge);
        assertThat(envelope).doesNotContain("Admin01", "AdminPass1!");
        mvc.perform(
                        post("/auth/login/secure")
                                .session(session)
                                .header(
                                        csrf.path("headerName").asText(),
                                        csrf.path("token").asText())
                                .contentType("application/json")
                                .content(envelope))
                .andExpect(status().isOk());
        mvc.perform(get("/auth/me").session(session))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.user.username").value("Admin01"))
                .andExpect(jsonPath("$.data.routes[0].children[0].moduleKey").value("user"));
        JsonNode freshCsrf =
                json.readTree(
                                mvc.perform(get("/auth/csrf").session(session))
                                        .andExpect(status().isOk())
                                        .andReturn()
                                        .getResponse()
                                        .getContentAsString())
                        .path("data");
        mvc.perform(
                        post("/auth/login/secure")
                                .session(session)
                                .header(
                                        freshCsrf.path("headerName").asText(),
                                        freshCsrf.path("token").asText())
                                .contentType("application/json")
                                .content(envelope))
                .andExpect(status().isBadRequest());
    }

    @Test
    void concurrentRedisSessionCopiesCannotConsumeTheSameChallengeTwice() throws Exception {
        var result =
                mvc.perform(get("/auth/login/challenge")).andExpect(status().isOk()).andReturn();
        var original = (MockHttpSession) result.getRequest().getSession(false);
        String attribute = LoginCipherService.class.getName() + ".challenge";
        Object saved = original.getAttribute(attribute);
        var envelope =
                json.readValue(
                        encrypt(
                                json.readTree(result.getResponse().getContentAsString())
                                        .path("data")),
                        EncryptedLoginDto.class);
        try (var workers = java.util.concurrent.Executors.newFixedThreadPool(2)) {
            var barrier = new java.util.concurrent.CyclicBarrier(2);
            java.util.List<java.util.concurrent.Callable<Boolean>> calls =
                    new java.util.ArrayList<>();
            for (int i = 0; i < 2; i++)
                calls.add(
                        () -> {
                            var copy = new MockHttpSession(null, original.getId());
                            copy.setAttribute(attribute, saved);
                            var request = new org.springframework.mock.web.MockHttpServletRequest();
                            request.setSession(copy);
                            barrier.await();
                            try {
                                return cipherService
                                        .decrypt(envelope, request)
                                        .getUsername()
                                        .equals("Admin01");
                            } catch (BusinessException rejected) {
                                return false;
                            }
                        });
            int accepted = 0;
            for (var future : workers.invokeAll(calls)) if (future.get()) accepted++;
            assertThat(accepted).isEqualTo(1);
        }
    }

    @Test
    void encryptedLoginSupportsConfiguredMaximumUnicodePassword() throws Exception {
        String password = "Aa1!" + "😀".repeat(124);
        passwords.validateNewPassword(password);
        new org.springframework.jdbc.core.JdbcTemplate(source)
                .update(
                        "UPDATE sys_user SET password=? WHERE username='Admin01'",
                        passwords.encode(password));
        var csrfResult = mvc.perform(get("/auth/csrf")).andReturn();
        var session = (MockHttpSession) csrfResult.getRequest().getSession(false);
        var csrf = json.readTree(csrfResult.getResponse().getContentAsString()).path("data");
        var challenge =
                json.readTree(
                                mvc.perform(get("/auth/login/challenge").session(session))
                                        .andExpect(status().isOk())
                                        .andReturn()
                                        .getResponse()
                                        .getContentAsString())
                        .path("data");
        mvc.perform(
                        post("/auth/login/secure")
                                .session(session)
                                .header(
                                        csrf.path("headerName").asText(),
                                        csrf.path("token").asText())
                                .contentType("application/json")
                                .content(encrypt(challenge, password)))
                .andExpect(status().isOk());
    }

    private String encrypt(JsonNode challenge) throws Exception {
        return encrypt(challenge, "AdminPass1!");
    }

    private String encrypt(JsonNode challenge, String password) throws Exception {
        return json.writeValueAsString(
                SecureLoginSupport.encrypt(json, challenge, "Admin01", password));
    }
}
