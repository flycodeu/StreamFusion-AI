package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.access.adapter.CameraHttpTransport;
import com.streamfusion.platform.camera.access.pojo.CameraScanRequest;
import com.streamfusion.platform.camera.access.service.CameraNetworkScan;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.net.ServerSocket;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

class CameraNetworkScanTest {
    @Test
    void boundsLiteralAddressesAndChecksEveryDestinationBeforeQueueing() {
        var properties = new CameraProperties();
        var policy = new CameraProperties.NetworkPolicy();
        policy.setCidrs(List.of("10.0.1.0/24"));
        properties.setNetworkPolicies(Map.of("test", policy));
        var scan =
                new CameraNetworkScan(
                        new CameraSourceRules(properties), mock(CameraHttpTransport.class));
        assertThat(scan.prepare(input("10.0.1.0/25", null, null, List.of(80, 443))).hosts())
                .hasSize(128);
        assertThat(scan.prepare(input(null, "10.0.1.1", "10.0.1.3", List.of(80))).hosts())
                .containsExactly("10.0.1.1", "10.0.1.2", "10.0.1.3");
        for (var value :
                List.of(
                        input("10.0.1.0/24", null, null, List.of(80)),
                        input("127.0.0.1/32", null, null, List.of(80)),
                        input(null, "10.0.1.250", "10.0.2.1", List.of(80)),
                        input(null, "10.0.1.2", "10.0.1.1", List.of(80)),
                        input(null, "camera.example", "camera.example", List.of(80)),
                        input("10.0.1.1/32", null, null, List.of(80, 80)),
                        input("10.0.1.1/32", null, null, List.of(80, 81, 82, 83, 84))))
            assertThatThrownBy(() -> scan.prepare(value)).isInstanceOf(RuntimeException.class);
    }

    @Test
    void scanConnectsWithoutProtocolBytesAndStopsOnRevokedActor() throws Exception {
        var rules = mock(CameraSourceRules.class);
        when(rules.host(anyString(), anyString())).thenAnswer(i -> i.getArgument(0));
        var transport = new CameraHttpTransport(rules);
        try (var server = new ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1"))) {
            var scan = new CameraNetworkScan(rules, transport);
            var plan =
                    new CameraNetworkScan.Plan(
                            "fixture", List.of("127.0.0.1"), List.of(server.getLocalPort()));
            var checks = new AtomicInteger();
            var result = scan.execute(plan, checks::incrementAndGet);
            assertThat(result.complete()).isTrue();
            assertThat(result.scannedTargets()).isOne();
            assertThat(result.hosts().getFirst().identityConfidence()).isEqualTo("PORT_OPEN_ONLY");
            try (var accepted = server.accept()) {
                assertThat(accepted.getInputStream().read()).isEqualTo(-1);
            }
            assertThat(checks.get()).isGreaterThanOrEqualTo(3);
            assertThatThrownBy(
                            () ->
                                    scan.execute(
                                            plan,
                                            () -> {
                                                throw new IllegalStateException("revoked");
                                            }))
                    .hasMessage("revoked");
        } finally {
            transport.close();
        }
    }

    private CameraScanRequest input(String cidr, String start, String end, List<Integer> ports) {
        return new CameraScanRequest("test", "test", cidr, start, end, ports);
    }
}
