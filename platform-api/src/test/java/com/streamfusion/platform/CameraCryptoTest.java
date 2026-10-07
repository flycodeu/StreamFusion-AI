package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.common.exception.BusinessException;
import java.util.Base64;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class CameraCryptoTest {
    private CameraProperties properties() {
        var properties = new CameraProperties();
        properties.setActiveKeyId("test");
        properties.getKeys().put("test", Base64.getEncoder().encodeToString(new byte[32]));
        var policy = new CameraProperties.NetworkPolicy();
        policy.setCidrs(List.of("10.0.0.0/8", "2001:db8::/32"));
        policy.setHosts(List.of("camera.example"));
        properties.getNetworkPolicies().put("test", policy);
        return properties;
    }

    @Test
    void envelopesBindIdentityUseDistinctNoncesAndPreserveEscapes() {
        var crypto = new CameraCryptoService(properties(), new ObjectMapper());
        var payload = Map.of("encodedPath", "/track%2F1", "rawQuery", "key=a%26b&x=+%2B");
        var first = crypto.encrypt("source/1/profile/2", payload, 16000);
        var second = crypto.encrypt("source/1/profile/2", payload, 16000);
        assertThat(first.nonce()).isNotEqualTo(second.nonce());
        assertThat(crypto.decrypt("source/1/profile/2", first).path("rawQuery").asText())
                .isEqualTo(payload.get("rawQuery"));
        assertThatThrownBy(() -> crypto.decrypt("source/1/profile/3", first))
                .isInstanceOf(BusinessException.class);
        first.tag()[0] ^= 1;
        assertThatThrownBy(() -> crypto.decrypt("source/1/profile/2", first))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void signaturesArePurposeBoundCanonicalAndSurviveActiveKeyRotation() {
        var props = properties();
        var crypto = new CameraCryptoService(props, new ObjectMapper());
        String signed = crypto.sign("move", Map.of("a", 1, "b", "2"));
        assertThat(crypto.verify("move", Map.of("b", "2", "a", 1), signed)).isTrue();
        assertThat(crypto.verify("scope", Map.of("a", 1, "b", "2"), signed)).isFalse();
        assertThat(crypto.verify("move", Map.of("a", 2, "b", "2"), signed)).isFalse();
        props.getKeys()
                .put(
                        "new",
                        Base64.getEncoder()
                                .encodeToString(
                                        "11111111111111111111111111111111"
                                                .getBytes(
                                                        java.nio.charset.StandardCharsets
                                                                .US_ASCII)));
        props.setActiveKeyId("new");
        assertThat(crypto.verify("move", Map.of("a", 1, "b", "2"), signed)).isTrue();
        props.getKeys().remove("test");
        assertThat(crypto.verify("move", Map.of("a", 1, "b", "2"), signed)).isFalse();
    }

    @Test
    void missingKeysFailClosedAndNeverGenerateTransientStorageKeys() {
        var crypto = new CameraCryptoService(new CameraProperties(), new ObjectMapper());
        assertThat(crypto.ready()).isFalse();
        assertThatThrownBy(() -> crypto.encrypt("x", Map.of("password", "secret"), 8192))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void destinationValidationNeverResolvesNamesOrAcceptsUrlSyntax() {
        var rules = new CameraSourceRules(properties());
        assertThat(rules.host("10.1.2.3", "test")).isEqualTo("10.1.2.3");
        assertThat(rules.host("CAMERA.EXAMPLE", "test")).isEqualTo("camera.example");
        assertThat(rules.host("2001:db8::1", "test")).contains("2001:db8");
        for (String rejected :
                List.of(
                        "127.0.0.1",
                        "169.254.169.254",
                        "10.01.2.3",
                        "10.999.2.3",
                        "user@10.1.2.3",
                        "10.1.2.3/path",
                        "unknown.example",
                        "localhost",
                        "::1",
                        "fe80::1%eth0"))
            assertThatThrownBy(() -> rules.host(rejected, "test"))
                    .as(rejected)
                    .isInstanceOf(BusinessException.class);
    }
}
