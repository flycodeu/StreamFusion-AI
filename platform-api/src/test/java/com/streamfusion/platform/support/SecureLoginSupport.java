package com.streamfusion.platform.support;

import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.captcha.CaptchaImageGenerator;
import com.streamfusion.platform.auth.captcha.CaptchaStore;
import com.streamfusion.platform.auth.guard.LoginGuardStore;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.SecureRandom;
import java.security.spec.ECGenParameterSpec;
import java.security.spec.X509EncodedKeySpec;
import java.util.Base64;
import java.util.HexFormat;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import javax.crypto.Cipher;
import javax.crypto.KeyAgreement;
import javax.crypto.Mac;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import org.junit.jupiter.api.BeforeEach;
import org.springframework.mock.web.MockHttpSession;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.request.MockHttpServletRequestBuilder;

/** Exercises the public encrypted login flow; only the external challenge store is substituted. */
public abstract class SecureLoginSupport {
    public static final String CAPTCHA_ANSWER = "ABC23";
    @MockitoBean private LoginGuardStore challengeGuard;
    @MockitoBean private CaptchaStore captchaStore;
    @MockitoBean private CaptchaImageGenerator captchaImages;

    @BeforeEach
    void resetChallengeStore() {
        claimEachChallengeOnce(challengeGuard);
        configureCaptchas(captchaStore, captchaImages);
    }

    public static void configureCaptchas(CaptchaStore store, CaptchaImageGenerator images) {
        when(images.generate())
                .thenReturn(
                        new CaptchaImageGenerator.Generated(
                                CAPTCHA_ANSWER, new CaptchaImageGenerator().generate().image()));
        record Stored(String id, String answerHash, byte[] image) {}
        var slots = new ConcurrentHashMap<String, Stored>();
        doAnswer(
                        call -> {
                            slots.put(
                                    call.getArgument(0),
                                    new Stored(
                                            call.getArgument(1),
                                            call.getArgument(2),
                                            call.getArgument(3)));
                            return null;
                        })
                .when(store)
                .replace(anyString(), anyString(), anyString(), any(byte[].class));
        when(store.image(anyString(), anyString()))
                .thenAnswer(
                        call -> {
                            var value = slots.get(call.getArgument(0));
                            return value != null && value.id().equals(call.getArgument(1))
                                    ? value.image()
                                    : null;
                        });
        when(store.consume(anyString(), anyString(), anyString()))
                .thenAnswer(
                        call -> {
                            var accepted = new java.util.concurrent.atomic.AtomicBoolean();
                            slots.computeIfPresent(
                                    call.getArgument(0),
                                    (key, value) -> {
                                        if (!value.id().equals(call.getArgument(1))) return value;
                                        accepted.set(
                                                value.answerHash().equals(call.getArgument(2)));
                                        return null;
                                    });
                            return accepted.get();
                        });
    }

    public static void claimEachChallengeOnce(LoginGuardStore store) {
        var consumed = ConcurrentHashMap.<String>newKeySet();
        when(store.claimChallenge(anyString()))
                .thenAnswer(call -> consumed.add(call.getArgument(0)));
    }

    public static MockHttpServletRequestBuilder loginRequest(
            MockMvc mvc,
            ObjectMapper json,
            MockHttpSession session,
            String username,
            String password)
            throws Exception {
        // MockHttpSession's default ID counter is not safe for concurrent login tests.
        if (session == null)
            session =
                    new MockHttpSession(
                            mvc.getDispatcherServlet().getServletContext(),
                            UUID.randomUUID().toString());
        var challengeRequest = get("/auth/login/challenge").session(session);
        var result = mvc.perform(challengeRequest).andExpect(status().isOk()).andReturn();
        var challenge = json.readTree(result.getResponse().getContentAsByteArray()).path("data");
        var anonymous = (MockHttpSession) result.getRequest().getSession(false);
        var captcha = captcha(mvc, json, anonymous);
        return post("/auth/login/secure")
                .session(anonymous)
                .contentType("application/json")
                .content(
                        json.writeValueAsBytes(
                                encrypt(
                                        json,
                                        challenge,
                                        username,
                                        password,
                                        captcha.path("captchaId").asText(),
                                        CAPTCHA_ANSWER)));
    }

    public static JsonNode captcha(MockMvc mvc, ObjectMapper json, MockHttpSession session)
            throws Exception {
        var csrfResult =
                mvc.perform(get("/auth/csrf").session(session))
                        .andExpect(status().isOk())
                        .andReturn();
        var csrf = json.readTree(csrfResult.getResponse().getContentAsByteArray()).path("data");
        var result =
                mvc.perform(
                                post("/auth/captcha")
                                        .session(session)
                                        .header(
                                                csrf.path("headerName").asText(),
                                                csrf.path("token").asText()))
                        .andExpect(status().isOk())
                        .andReturn();
        return json.readTree(result.getResponse().getContentAsByteArray()).path("data");
    }

    /** Mirrors the browser's ECDH/HKDF/AES-GCM envelope without calling server decryption code. */
    public static Map<String, String> encrypt(
            ObjectMapper json, JsonNode challenge, String username, String password)
            throws Exception {
        return encrypt(json, challenge, Map.of("username", username, "password", password));
    }

    public static Map<String, String> encrypt(
            ObjectMapper json,
            JsonNode challenge,
            String username,
            String password,
            String captchaId,
            String captchaAnswer)
            throws Exception {
        return encrypt(
                json,
                challenge,
                Map.of(
                        "username",
                        username,
                        "password",
                        password,
                        "captchaId",
                        captchaId,
                        "captchaAnswer",
                        captchaAnswer));
    }

    private static Map<String, String> encrypt(
            ObjectMapper json, JsonNode challenge, Map<String, String> credentials)
            throws Exception {
        String id = challenge.path("challengeId").asText();
        byte[] server = Base64.getUrlDecoder().decode(challenge.path("serverPublicKey").asText());
        var generator = KeyPairGenerator.getInstance("EC");
        generator.initialize(new ECGenParameterSpec("secp256r1"));
        var pair = generator.generateKeyPair();
        var agreement = KeyAgreement.getInstance("ECDH");
        agreement.init(pair.getPrivate());
        agreement.doPhase(
                KeyFactory.getInstance("EC").generatePublic(new X509EncodedKeySpec(server)), true);
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
        byte[] ciphertext = cipher.doFinal(json.writeValueAsBytes(credentials));
        var encoder = Base64.getUrlEncoder().withoutPadding();
        return Map.of(
                "challengeId", id,
                "clientPublicKey", encoder.encodeToString(pair.getPublic().getEncoded()),
                "iv", encoder.encodeToString(iv),
                "ciphertext", encoder.encodeToString(ciphertext));
    }
}
