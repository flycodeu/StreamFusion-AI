package com.streamfusion.platform.server.config;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.springframework.boot.context.properties.bind.Bindable;
import org.springframework.boot.context.properties.bind.Binder;
import org.springframework.boot.context.properties.source.MapConfigurationPropertySource;

class ServerMonitorPropertiesTest {
    @Test
    void defaultsToTheDevelopmentWebListener() {
        var properties = bind(Map.of());
        assertThat(properties.webHost()).isEqualTo("127.0.0.1");
        assertThat(properties.webPort()).isEqualTo(8090);
    }

    @ParameterizedTest
    @CsvSource({"web.internal,8443", "127.0.0.1,0", "'',8090", "::1,8090"})
    void supportsExplicitWebDeploymentEndpointsAndDisabledProbe(String host, int port) {
        var properties = bind(Map.of("web-host", host, "web-port", port));
        assertThat(properties.webHost()).isEqualTo(host);
        assertThat(properties.webPort()).isEqualTo(port);
    }

    @ParameterizedTest
    @CsvSource({"127.0.0.1,-1", "127.0.0.1,65536", "https://web.internal,443"})
    void rejectsAnInvalidWebPortOrUrlInsteadOfProbingIt(String host, int port) {
        assertThatThrownBy(() -> bind(Map.of("web-host", host, "web-port", port)))
                .hasRootCauseInstanceOf(IllegalArgumentException.class);
    }

    private static ServerMonitorProperties bind(Map<String, Object> values) {
        var source = new MapConfigurationPropertySource();
        values.forEach((key, value) -> source.put("platform.server-monitor." + key, value));
        return new Binder(source)
                .bindOrCreate(
                        "platform.server-monitor", Bindable.of(ServerMonitorProperties.class));
    }
}
