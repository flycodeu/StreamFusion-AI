package com.streamfusion.platform.auth.service;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.guard.LoginGuardStore;
import com.streamfusion.platform.auth.pojo.dto.EncryptedLoginDto;
import com.streamfusion.platform.auth.pojo.dto.LoginDto;
import com.streamfusion.platform.auth.pojo.vo.LoginChallengeVo;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpSession;
import java.io.Serializable;
import java.nio.charset.StandardCharsets;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.SecureRandom;
import java.security.spec.ECGenParameterSpec;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.Base64;
import javax.crypto.Cipher;
import javax.crypto.KeyAgreement;
import javax.crypto.Mac;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** Per-session, one-use P-256 ECDH challenge; the private key expires with the challenge. */
@Service
@RequiredArgsConstructor
public class LoginCipherService {
    private static final String ATTRIBUTE = LoginCipherService.class.getName() + ".challenge";
    private static final byte[] INFO = "streamfusion-login-v1".getBytes(StandardCharsets.US_ASCII);
    private final Clock clock;
    private final ObjectMapper json;
    private final LoginGuardStore loginGuard;
    private final SecureRandom random = new SecureRandom();

    private record Challenge(String id, byte[] privateKey, Instant expiresAt)
            implements Serializable {}

    public LoginChallengeVo challenge(HttpServletRequest request) {
        try {
            var generator = KeyPairGenerator.getInstance("EC");
            generator.initialize(new ECGenParameterSpec("secp256r1"), random);
            var pair = generator.generateKeyPair();
            byte[] id = new byte[16];
            random.nextBytes(id);
            String challengeId = hex(id);
            request.getSession(true)
                    .setAttribute(
                            ATTRIBUTE,
                            new Challenge(
                                    challengeId,
                                    pair.getPrivate().getEncoded(),
                                    clock.instant().plusSeconds(120)));
            return new LoginChallengeVo(challengeId, encode(pair.getPublic().getEncoded()));
        } catch (Exception ex) {
            if (com.streamfusion.platform.auth.security.SessionDependencyFilter.dependencyFailure(
                    ex)) throw BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
            throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        }
    }

    public LoginDto decrypt(EncryptedLoginDto input, HttpServletRequest request) {
        HttpSession session = request.getSession(false);
        if (session == null) throw invalid();
        Object saved = session.getAttribute(ATTRIBUTE);
        // Consume before validation, including malformed ciphertext and failed credentials.
        session.removeAttribute(ATTRIBUTE);
        if (!(saved instanceof Challenge challenge)
                || input == null
                || !challenge.id().equals(input.challengeId())
                || !clock.instant().isBefore(challenge.expiresAt())) throw invalid();
        if (!loginGuard.claimChallenge(challenge.id())) throw invalid();
        try {
            byte[] publicBytes = decode(input.clientPublicKey(), 200);
            byte[] iv = decode(input.iv(), 12);
            byte[] encrypted = decode(input.ciphertext(), 2064);
            if (iv.length != 12 || encrypted.length < 17) throw invalid();
            var keys = KeyFactory.getInstance("EC");
            var agreement = KeyAgreement.getInstance("ECDH");
            agreement.init(keys.generatePrivate(new PKCS8EncodedKeySpec(challenge.privateKey())));
            agreement.doPhase(keys.generatePublic(new X509EncodedKeySpec(publicBytes)), true);
            byte[] secret = agreement.generateSecret();
            byte[] salt = hexBytes(challenge.id());
            byte[] key = hkdf(secret, salt);
            var cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(
                    Cipher.DECRYPT_MODE,
                    new SecretKeySpec(key, "AES"),
                    new GCMParameterSpec(128, iv));
            cipher.updateAAD(challenge.id().getBytes(StandardCharsets.US_ASCII));
            byte[] clear = cipher.doFinal(encrypted);
            // Includes a 128-code-point password serialized as escaped UTF-16 surrogate pairs.
            if (clear.length > 2048) throw invalid();
            LoginDto login = json.readValue(clear, LoginDto.class);
            if (login.getUsername() == null || login.getPassword() == null) throw invalid();
            return login;
        } catch (BusinessException ex) {
            throw ex;
        } catch (Exception ex) {
            // Never reveal which part of the encrypted credential failed.
            throw invalid();
        }
    }

    private static byte[] hkdf(byte[] secret, byte[] salt) throws Exception {
        var mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(salt, "HmacSHA256"));
        byte[] prk = mac.doFinal(secret);
        mac.init(new SecretKeySpec(prk, "HmacSHA256"));
        mac.update(INFO);
        mac.update((byte) 1);
        return mac.doFinal();
    }

    private static byte[] decode(String value, int maxBytes) {
        if (value == null || value.length() > maxBytes * 2 || !value.matches("[A-Za-z0-9_-]+"))
            throw invalid();
        try {
            byte[] bytes = Base64.getUrlDecoder().decode(value);
            if (bytes.length > maxBytes) throw invalid();
            return bytes;
        } catch (IllegalArgumentException ex) {
            throw invalid();
        }
    }

    private static String encode(byte[] bytes) {
        return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
    }

    private static String hex(byte[] bytes) {
        return java.util.HexFormat.of().formatHex(bytes);
    }

    private static byte[] hexBytes(String value) {
        return java.util.HexFormat.of().parseHex(value);
    }

    private static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }
}
