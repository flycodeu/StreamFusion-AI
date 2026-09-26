package com.streamfusion.platform.support;

import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
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
    @MockitoBean private LoginGuardStore challengeGuard;

    @BeforeEach
    void resetChallengeStore() {
        claimEachChallengeOnce(challengeGuard);
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
        var challengeRequest = get("/auth/login/challenge");
        if (session != null) challengeRequest.session(session);
        var result = mvc.perform(challengeRequest).andExpect(status().isOk()).andReturn();
        var challenge = json.readTree(result.getResponse().getContentAsByteArray()).path("data");
        return post("/auth/login/secure")
                .session((MockHttpSession) result.getRequest().getSession(false))
                .contentType("application/json")
                .content(json.writeValueAsBytes(encrypt(json, challenge, username, password)));
    }

    /** Mirrors the browser's ECDH/HKDF/AES-GCM envelope without calling server decryption code. */
    public static Map<String, String> encrypt(
            ObjectMapper json, JsonNode challenge, String username, String password)
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
        byte[] ciphertext =
                cipher.doFinal(
                        json.writeValueAsBytes(Map.of("username", username, "password", password)));
        var encoder = Base64.getUrlEncoder().withoutPadding();
        return Map.of(
                "challengeId", id,
                "clientPublicKey", encoder.encodeToString(pair.getPublic().getEncoded()),
                "iv", encoder.encodeToString(iv),
                "ciphertext", encoder.encodeToString(ciphertext));
    }
}
