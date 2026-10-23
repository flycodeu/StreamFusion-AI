package com.streamfusion.platform.storage.s3;

import static org.assertj.core.api.Assertions.*;

import com.streamfusion.platform.storage.StorageProperties;
import com.streamfusion.platform.storage.StorageRules;
import java.net.URI;
import java.util.List;
import org.junit.jupiter.api.Test;

class StorageNetworkTest {
    @Test
    void localStorageWorksWithoutACameraPolicyButOtherPortsAndDestinationsRemainBlocked() {
        try (var ignored = new NetworkFixture()) {
            var network = ignored.network;
            assertThat(
                            network.resolve(
                                            URI.create("http://127.0.0.1:8333"),
                                            "obsolete-camera-policy")
                                    .isLoopbackAddress())
                    .isTrue();
            assertThat(network.resolve(URI.create("http://localhost:8333"), "").isLoopbackAddress())
                    .isTrue();
            for (String url :
                    List.of(
                            "http://127.0.0.1:8080",
                            "http://192.168.142.1:8333",
                            "https://127.0.0.1:8333",
                            "http://169.254.169.254"))
                assertThatThrownBy(() -> network.resolve(URI.create(url), "simulation"))
                        .isInstanceOf(StorageRemoteException.class)
                        .hasMessage("STORAGE_NETWORK_DENIED");
        }
    }

    @Test
    void deploymentCanExplicitlyRegisterAnAdditionalStorageOrigin() {
        var properties = new StorageProperties();
        properties.setAllowedEndpoints(List.of("http://192.168.142.1:8333"));
        var network = new StorageNetwork(properties);
        try {
            assertThatCode(
                            () ->
                                    network.validateEndpoint(
                                            URI.create("http://192.168.142.1:8333/metrics")))
                    .doesNotThrowAnyException();
            assertThatThrownBy(
                            () -> network.validateEndpoint(URI.create("http://192.168.142.1:8080")))
                    .isInstanceOf(RuntimeException.class);
        } finally {
            network.close();
        }
    }

    @Test
    void consoleLinksAllowHttpPathsButRejectActiveSchemesCredentialsAndQuerySecrets() {
        assertThat(StorageRules.managementUrl("http://127.0.0.1:23646/admin"))
                .isEqualTo("http://127.0.0.1:23646/admin");
        assertThat(StorageRules.managementUrl("")).isNull();
        for (String url :
                List.of(
                        "javascript:alert(1)",
                        "//example.com",
                        "http://user:password@localhost",
                        "http://localhost/?token=secret",
                        "http://localhost/#secret",
                        "http://localhost:0"))
            assertThatThrownBy(() -> StorageRules.managementUrl(url))
                    .isInstanceOf(RuntimeException.class);
    }

    private static class NetworkFixture implements AutoCloseable {
        final StorageNetwork network = new StorageNetwork(new StorageProperties());

        public void close() {
            network.close();
        }
    }
}
