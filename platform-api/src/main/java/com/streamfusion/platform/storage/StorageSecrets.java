package com.streamfusion.platform.storage;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.storage.entity.StorageInstance;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Reuses the configured envelope/key rotation facility with a separate storage AAD and purpose. */
@Component
@RequiredArgsConstructor
public class StorageSecrets {
    private final CameraCryptoService crypto;

    public boolean ready() {
        return crypto.ready();
    }

    public JsonNode read(StorageInstance row) {
        return crypto.decrypt(
                "storage_instance/" + row.getId(),
                new CameraCryptoService.Envelope(
                        row.getEncryptionKeyId(),
                        row.getSecretNonce(),
                        row.getSecretCiphertext(),
                        row.getSecretTag()));
    }

    public void save(StorageInstance row, String access, String secret) {
        var envelope =
                crypto.encrypt(
                        "storage_instance/" + row.getId(),
                        java.util.Map.of("accessKey", access, "secretKey", secret),
                        8192);
        row.setEncryptionKeyId(envelope.keyId());
        row.setSecretNonce(envelope.nonce());
        row.setSecretCiphertext(envelope.ciphertext());
        row.setSecretTag(envelope.tag());
    }

    public String fingerprint(StorageInstance row) {
        return crypto.sign(
                "storage-connection",
                java.util.Map.of(
                        "endpoint",
                        row.getEndpoint(),
                        "region",
                        row.getRegion(),
                        "policy",
                        row.getNetworkPolicyKey(),
                        "keyId",
                        row.getEncryptionKeyId(),
                        "cipher",
                        row.getSecretCiphertext()));
    }

    public String sign(Object body) {
        return crypto.sign("storage-token", body);
    }

    public boolean verify(Object body, String signature) {
        return crypto.verify("storage-token", body, signature);
    }
}
