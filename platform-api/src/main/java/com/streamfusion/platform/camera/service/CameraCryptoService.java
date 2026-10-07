package com.streamfusion.platform.camera.service;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.Arrays;
import java.util.Base64;
import java.util.List;
import javax.crypto.Cipher;
import javax.crypto.Mac;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** AES-GCM envelopes and purpose-separated HMACs; never reuses ephemeral login keys. */
@Service
@RequiredArgsConstructor
public class CameraCryptoService {
    private static final SecureRandom RANDOM = new SecureRandom();
    private final CameraProperties properties;
    private final ObjectMapper json;

    public boolean ready() {
        try {
            key(activeKeyId(), "encryption");
            return true;
        } catch (BusinessException ex) {
            return false;
        }
    }

    public String activeKeyId() {
        String id = properties.getActiveKeyId();
        if (id == null
                || !id.matches("[A-Za-z0-9_-]{1,64}")
                || !properties.getKeys().containsKey(id)) throw unavailable();
        return id;
    }

    public List<String> keyIds() {
        return List.copyOf(properties.getKeys().keySet());
    }

    public String canonical(Object value) {
        return ordered(json.valueToTree(value)).toString();
    }

    private JsonNode ordered(JsonNode value) {
        if (value.isObject()) {
            var object = json.createObjectNode();
            value.properties().stream()
                    .sorted(java.util.Map.Entry.comparingByKey())
                    .forEach(entry -> object.set(entry.getKey(), ordered(entry.getValue())));
            return object;
        }
        if (value.isArray()) {
            var array = json.createArrayNode();
            value.forEach(item -> array.add(ordered(item)));
            return array;
        }
        return value;
    }

    public byte[] digest(String keyId, String purpose, Object value) {
        return hmac(key(keyId, purpose), canonical(value).getBytes(StandardCharsets.UTF_8));
    }

    public String sign(String purpose, Object value) {
        String keyId = activeKeyId();
        return keyId
                + "."
                + Base64.getUrlEncoder()
                        .withoutPadding()
                        .encodeToString(digest(keyId, purpose, value));
    }

    public boolean verify(String purpose, Object value, String signature) {
        if (signature == null || signature.length() > 160) return false;
        String[] parts = signature.split("\\.", -1);
        if (parts.length != 2) return false;
        try {
            return MessageDigest.isEqual(
                    digest(parts[0], purpose, value), Base64.getUrlDecoder().decode(parts[1]));
        } catch (IllegalArgumentException | BusinessException ex) {
            return false;
        }
    }

    public Envelope encrypt(String aad, Object value, int maxBytes) {
        String keyId = activeKeyId();
        byte[] plain = canonical(value).getBytes(StandardCharsets.UTF_8);
        if (plain.length > maxBytes) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        byte[] nonce = new byte[12];
        RANDOM.nextBytes(nonce);
        try {
            Cipher cipher = cipher(Cipher.ENCRYPT_MODE, keyId, nonce, aad);
            byte[] encrypted = cipher.doFinal(plain);
            return new Envelope(
                    keyId,
                    nonce,
                    Arrays.copyOf(encrypted, encrypted.length - 16),
                    Arrays.copyOfRange(encrypted, encrypted.length - 16, encrypted.length));
        } catch (java.security.GeneralSecurityException ex) {
            throw unavailable();
        } finally {
            Arrays.fill(plain, (byte) 0);
        }
    }

    public JsonNode decrypt(String aad, Envelope envelope) {
        byte[] plain = null;
        try {
            byte[] encrypted = new byte[envelope.ciphertext().length + 16];
            System.arraycopy(envelope.ciphertext(), 0, encrypted, 0, envelope.ciphertext().length);
            System.arraycopy(envelope.tag(), 0, encrypted, envelope.ciphertext().length, 16);
            plain =
                    cipher(Cipher.DECRYPT_MODE, envelope.keyId(), envelope.nonce(), aad)
                            .doFinal(encrypted);
            return json.readTree(plain);
        } catch (Exception ex) {
            throw unavailable();
        } finally {
            if (plain != null) Arrays.fill(plain, (byte) 0);
        }
    }

    private Cipher cipher(int mode, String keyId, byte[] nonce, String aad)
            throws java.security.GeneralSecurityException {
        Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
        cipher.init(
                mode,
                new SecretKeySpec(key(keyId, "encryption"), "AES"),
                new GCMParameterSpec(128, nonce));
        cipher.updateAAD(aad.getBytes(StandardCharsets.UTF_8));
        return cipher;
    }

    private byte[] key(String keyId, String purpose) {
        try {
            String encoded = properties.getKeys().get(keyId);
            if (encoded == null) throw unavailable();
            byte[] master = Base64.getDecoder().decode(encoded);
            if (master.length != 32) throw unavailable();
            try {
                return hmac(
                        master,
                        ("StreamFusion/camera/v1/" + purpose).getBytes(StandardCharsets.UTF_8));
            } finally {
                Arrays.fill(master, (byte) 0);
            }
        } catch (IllegalArgumentException ex) {
            throw unavailable();
        }
    }

    private static byte[] hmac(byte[] key, byte[] value) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(key, "HmacSHA256"));
            return mac.doFinal(value);
        } catch (java.security.GeneralSecurityException ex) {
            throw unavailable();
        }
    }

    private static BusinessException unavailable() {
        return BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
    }

    public record Envelope(String keyId, byte[] nonce, byte[] ciphertext, byte[] tag) {
        @Override
        public String toString() {
            return "Envelope[redacted]";
        }
    }
}
