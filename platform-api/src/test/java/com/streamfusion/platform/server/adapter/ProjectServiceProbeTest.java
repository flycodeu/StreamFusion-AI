package com.streamfusion.platform.server.adapter;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.server.config.ServerMonitorProperties;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.time.Duration;
import org.junit.jupiter.api.Test;

class ProjectServiceProbeTest {
    @Test
    void projectsDatabaseEndpointWithoutCredentialsOrQueryParameters() {
        var endpoint =
                ProjectServiceProbe.mysql(
                        "jdbc:mysql://db.example:3307/project?user=private&password=secret");
        assertThat(endpoint.host()).isEqualTo("db.example");
        assertThat(endpoint.port()).isEqualTo(3307);
        assertThat(ProjectServiceProbe.mysql("jdbc:h2:mem:test").host()).isNull();
    }

    @Test
    void separatesMissingConfigurationFailedDnsAndReachableTcp() throws Exception {
        var resolver = mock(BoundedHostResolver.class);
        var probe = new ProjectServiceProbe(properties(), resolver);
        assertThat(probe.probe("web", "Platform Web", "127.0.0.1", 0).status())
                .isEqualTo("NOT_CONFIGURED");
        assertThat(probe.probe("web", "Platform Web", "", 8090).status())
                .isEqualTo("NOT_CONFIGURED");
        verifyNoInteractions(resolver);
        assertThat(probe.probe("agent", "Agent", "", 0).status()).isEqualTo("NOT_CONFIGURED");
        assertThat(probe.probe("agent", "Agent", "dns.example", 81).status())
                .isEqualTo("UNAVAILABLE");
        var loopback = InetAddress.getLoopbackAddress();
        when(resolver.resolve(any(), any())).thenReturn(loopback);
        try (var server = new ServerSocket(0, 1, loopback)) {
            assertThat(probe.probe("agent", "Agent", "loopback", server.getLocalPort()).status())
                    .isEqualTo("REACHABLE");
        }
    }

    private ServerMonitorProperties properties() {
        return new ServerMonitorProperties(
                Duration.ofSeconds(30),
                Duration.ofMillis(100),
                Duration.ofSeconds(6),
                "127.0.0.1",
                0,
                "127.0.0.1",
                0,
                "127.0.0.1",
                0,
                false);
    }
}
