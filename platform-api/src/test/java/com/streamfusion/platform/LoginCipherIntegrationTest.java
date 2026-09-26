package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.guard.LoginGuardStore;
import com.streamfusion.platform.auth.pojo.dto.EncryptedLoginDto;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.auth.service.LoginCipherService;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.support.IdentitySchema;
import java.nio.charset.StandardCharsets;
import java.security.KeyPairGenerator;
import java.security.SecureRandom;
import java.security.spec.ECGenParameterSpec;
import java.security.spec.X509EncodedKeySpec;
import java.util.Base64;
import java.util.HexFormat;
import java.util.Map;
import javax.crypto.Cipher;
import javax.crypto.KeyAgreement;
import javax.crypto.Mac;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;

@SpringBootTest(
        properties = {
            "spring.datasource.url=jdbc:h2:mem:encrypted_login;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "platform.auth.allow-legacy-login=false",
            "platform.auth.max-password-length=128"
        })
@AutoConfigureMockMvc
@ActiveProfiles("test")
class LoginCipherIntegrationTest {
    @Autowired MockMvc mvc;
    @Autowired ObjectMapper json;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired LoginCipherService cipherService;
    @Autowired PasswordService passwords;
    @MockitoBean LoginGuardStore loginGuard;

    @BeforeEach
    void reset() throws Exception {
        IdentitySchema.initialize(source);
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        var consumed = java.util.concurrent.ConcurrentHashMap.<String>newKeySet();
        when(loginGuard.claimChallenge(anyString()))
                .thenAnswer(call -> consumed.add(call.getArgument(0)));
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
                .andExpect(status().isMethodNotAllowed());

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
        String id = challenge.path("challengeId").asText();
        byte[] server = Base64.getUrlDecoder().decode(challenge.path("serverPublicKey").asText());
        var generator = KeyPairGenerator.getInstance("EC");
        generator.initialize(new ECGenParameterSpec("secp256r1"));
        var pair = generator.generateKeyPair();
        var agreement = KeyAgreement.getInstance("ECDH");
        agreement.init(pair.getPrivate());
        agreement.doPhase(
                java.security.KeyFactory.getInstance("EC")
                        .generatePublic(new X509EncodedKeySpec(server)),
                true);
        var mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(HexFormat.of().parseHex(id), "HmacSHA256"));
        byte[] prk = mac.doFinal(agreement.generateSecret());
        mac.init(new SecretKeySpec(prk, "HmacSHA256"));
        mac.update("streamfusion-login-v1".getBytes(StandardCharsets.US_ASCII));
        mac.update((byte) 1);
        byte[] iv = new byte[12];
        new SecureRandom().nextBytes(iv);
        var cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(
                Cipher.ENCRYPT_MODE,
                new SecretKeySpec(mac.doFinal(), "AES"),
                new GCMParameterSpec(128, iv));
        cipher.updateAAD(id.getBytes(StandardCharsets.US_ASCII));
        byte[] ciphertext =
                cipher.doFinal(
                        json.writeValueAsBytes(
                                Map.of("username", "Admin01", "password", password)));
        var encoder = Base64.getUrlEncoder().withoutPadding();
        return json.writeValueAsString(
                Map.of(
                        "challengeId", id,
                        "clientPublicKey", encoder.encodeToString(pair.getPublic().getEncoded()),
                        "iv", encoder.encodeToString(iv),
                        "ciphertext", encoder.encodeToString(ciphertext)));
    }
}
