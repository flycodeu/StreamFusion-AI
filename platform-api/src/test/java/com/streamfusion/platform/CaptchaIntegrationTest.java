package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.doThrow;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.captcha.CaptchaStore;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.support.SecureLoginSupport;
import java.io.ByteArrayInputStream;
import java.time.Instant;
import javax.imageio.ImageIO;
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
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.ResultActions;

@SpringBootTest(
        properties = "spring.datasource.url=jdbc:h2:mem:captcha;MODE=MySQL;DB_CLOSE_DELAY=-1")
@AutoConfigureMockMvc
@ActiveProfiles("test")
class CaptchaIntegrationTest extends SecureLoginSupport {
    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired CaptchaStore store;
    private JdbcTemplate jdbc;

    @BeforeEach
    void setup() throws Exception {
        IdentitySchema.initialize(source);
        jdbc = new JdbcTemplate(source);
        bootstrap.initialize("CaptchaAdmin", null, "CaptchaPass1!", null);
    }

    @Test
    void csrfAndSessionBindTheNonCacheableImageAndRefreshInvalidatesThePreviousId()
            throws Exception {
        var session = anonymous();
        mvc.perform(post("/auth/captcha").session(session)).andExpect(status().isForbidden());
        Instant before = Instant.now();
        JsonNode first = captcha(mvc, json, session);
        String id = first.path("captchaId").asText();
        String imageUrl = first.path("imageUrl").asText();
        assertThat(id).matches("[0-9a-f]{32}");
        assertThat(imageUrl).isEqualTo("/auth/captcha/" + id + "/image");
        assertThat(Instant.parse(first.path("expiresAt").asText()))
                .isBetween(before.plusSeconds(119), Instant.now().plusSeconds(121));
        assertThat(first.size()).isEqualTo(3);
        for (int attempt = 0; attempt < 2; attempt++) {
            var response =
                    mvc.perform(get(imageUrl).session(session))
                            .andExpect(status().isOk())
                            .andExpect(content().contentType("image/png"))
                            .andExpect(header().string("Cache-Control", "no-store"))
                            .andExpect(header().string("X-Content-Type-Options", "nosniff"))
                            .andReturn()
                            .getResponse();
            var image = ImageIO.read(new ByteArrayInputStream(response.getContentAsByteArray()));
            assertThat(image.getWidth()).isEqualTo(168);
            assertThat(image.getHeight()).isEqualTo(56);
        }
        mvc.perform(get(imageUrl))
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value("CAPTCHA_INVALID"));
        mvc.perform(get(imageUrl).session(anonymous())).andExpect(status().isBadRequest());
        JsonNode current = captcha(mvc, json, session);
        assertThat(current.path("captchaId").asText()).isNotEqualTo(id);
        mvc.perform(get(imageUrl).session(session)).andExpect(status().isBadRequest());
        submit(session, id, CAPTCHA_ANSWER, "CaptchaPass1!").andExpect(status().isBadRequest());
        submit(
                        session,
                        current.path("captchaId").asText(),
                        CAPTCHA_ANSWER.toLowerCase(),
                        "CaptchaPass1!")
                .andExpect(status().isOk());
        mvc.perform(get(current.path("imageUrl").asText()).session(session))
                .andExpect(status().isConflict());
    }

    @Test
    void missingAndIncorrectCaptchaCannotReachPasswordVerificationAndEveryAttemptConsumesItsCode()
            throws Exception {
        var session = anonymous();
        String id = captcha(mvc, json, session).path("captchaId").asText();
        submit(session, id, "ZZZZZ", "WrongPass1!")
                .andExpect(status().isBadRequest())
                .andExpect(jsonPath("$.code").value("CAPTCHA_INVALID"));
        assertThat(failures()).isZero();
        submit(session, id, CAPTCHA_ANSWER, "CaptchaPass1!").andExpect(status().isBadRequest());
        submit(session, null, null, "CaptchaPass1!").andExpect(status().isBadRequest());
        id = captcha(mvc, json, session).path("captchaId").asText();
        submit(session, id, CAPTCHA_ANSWER, "WrongPass1!")
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.code").value("LOGIN_FAILED"));
        assertThat(failures()).isEqualTo(1);
        submit(session, id, CAPTCHA_ANSWER, "CaptchaPass1!").andExpect(status().isBadRequest());
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_login_record", Integer.class))
                .isZero();
        id = captcha(mvc, json, session).path("captchaId").asText();
        submit(session, id, CAPTCHA_ANSWER, "CaptchaPass1!").andExpect(status().isOk());
        assertThat(failures()).isZero();
    }

    @Test
    void unavailableRedisDoesNotIssueAnUnverifiableCaptchaOrRevealDiagnostics() throws Exception {
        var session = anonymous();
        var csrf = csrf(session);
        doThrow(new DataAccessResourceFailureException("private captcha backend detail"))
                .when(store)
                .replace(anyString(), anyString(), anyString(), any(byte[].class));
        var response =
                mvc.perform(
                                post("/auth/captcha")
                                        .session(session)
                                        .header(
                                                csrf.path("headerName").asText(),
                                                csrf.path("token").asText()))
                        .andExpect(status().isServiceUnavailable())
                        .andExpect(jsonPath("$.code").value("DEPENDENCY_UNAVAILABLE"))
                        .andExpect(jsonPath("$.data").isEmpty())
                        .andReturn()
                        .getResponse();
        assertThat(response.getContentAsString()).doesNotContain("private captcha", CAPTCHA_ANSWER);
    }

    private int failures() {
        return jdbc.queryForObject(
                "SELECT failed_login_count FROM sys_user WHERE username='CaptchaAdmin'",
                Integer.class);
    }

    private MockHttpSession anonymous() throws Exception {
        return (MockHttpSession)
                mvc.perform(get("/auth/csrf"))
                        .andExpect(status().isOk())
                        .andReturn()
                        .getRequest()
                        .getSession(false);
    }

    private JsonNode csrf(MockHttpSession session) throws Exception {
        return json.readTree(
                        mvc.perform(get("/auth/csrf").session(session))
                                .andExpect(status().isOk())
                                .andReturn()
                                .getResponse()
                                .getContentAsByteArray())
                .path("data");
    }

    private ResultActions submit(MockHttpSession session, String id, String answer, String password)
            throws Exception {
        var csrf = csrf(session);
        var challenge =
                json.readTree(
                                mvc.perform(get("/auth/login/challenge").session(session))
                                        .andExpect(status().isOk())
                                        .andReturn()
                                        .getResponse()
                                        .getContentAsByteArray())
                        .path("data");
        var encrypted =
                id == null
                        ? encrypt(json, challenge, "CaptchaAdmin", password)
                        : encrypt(json, challenge, "CaptchaAdmin", password, id, answer);
        String body = json.writeValueAsString(encrypted);
        assertThat(body)
                .doesNotContain("CaptchaPass1!", "WrongPass1!", "captchaAnswer", CAPTCHA_ANSWER);
        return mvc.perform(
                post("/auth/login/secure")
                        .session(session)
                        .header(csrf.path("headerName").asText(), csrf.path("token").asText())
                        .contentType("application/json")
                        .content(body));
    }
}
