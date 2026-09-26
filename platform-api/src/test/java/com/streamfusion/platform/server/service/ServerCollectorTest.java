package com.streamfusion.platform.server.service;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.server.adapter.HostMetricsAdapter;
import com.streamfusion.platform.server.adapter.JvmMetricsSampler;
import com.streamfusion.platform.server.adapter.NvidiaGpuAdapter;
import com.streamfusion.platform.server.adapter.ProjectServiceProbe;
import com.streamfusion.platform.server.config.ServerMonitorProperties;
import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.time.Clock;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicLong;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.springframework.mock.env.MockEnvironment;

class ServerCollectorTest {
    @Test
    void coordinatesWarmupAndWindowBeforeCallingAdaptersAndHonorsDisabledGpu() {
        var host = mock(HostMetricsAdapter.class);
        var jvm = mock(JvmMetricsSampler.class);
        var gpu = mock(NvidiaGpuAdapter.class);
        var probes = mock(ProjectServiceProbe.class);
        var started = new AtomicLong();
        when(host.beginCpuSample())
                .thenAnswer(
                        call -> {
                            started.set(System.nanoTime());
                            return new HostMetricsAdapter.CpuSample(started.get(), 4);
                        });
        when(host.finishCpuSample(any()))
                .thenAnswer(
                        call -> {
                            assertThat(System.nanoTime() - started.get())
                                    .isGreaterThanOrEqualTo(
                                            HostMetricsAdapter.CPU_WINDOW.toNanos());
                            return new ServerSnapshotVo.Host("Test", "amd64", 4, 12.5, 100L, 60L);
                        });
        when(probes.probe(any(), any(), any(), any()))
                .thenReturn(
                        new ServerSnapshotVo.ProjectService(
                                "test", "Test", null, null, "NOT_CONFIGURED"));
        var properties =
                new ServerMonitorProperties(
                        Duration.ofSeconds(30),
                        Duration.ofMillis(100),
                        Duration.ofSeconds(6),
                        "127.0.0.1",
                        0,
                        "127.0.0.1",
                        0,
                        "127.0.0.1",
                        8090,
                        false);
        var environment =
                new MockEnvironment()
                        .withProperty("spring.datasource.url", "jdbc:mysql://127.0.0.1:3306/test");
        var collector =
                new ServerCollector(
                        properties, environment, Clock.systemUTC(), host, jvm, gpu, probes);
        var sample = collector.collect();
        assertThat(sample.host().cpuPercent()).isEqualTo(12.5);
        assertThat(sample.gpuStatus()).isEqualTo("DISABLED");
        assertThat(sample.services()).hasSize(6);
        verify(probes).probe("web", "Platform Web", "127.0.0.1", 8090);
        verifyNoInteractions(gpu);
        var order = inOrder(host, jvm);
        order.verify(host).beginCpuSample();
        order.verify(jvm).beginCpuSample();
        order.verify(host).finishCpuSample(any());
        order.verify(jvm).finishCpuSample(any());
    }

    @ParameterizedTest
    @CsvSource({"web.internal,8443,REACHABLE", "127.0.0.1,0,NOT_CONFIGURED"})
    void includesTheConfiguredWebEndpointWithTheProbeStatus(String host, int port, String status) {
        var probes = mock(ProjectServiceProbe.class);
        var web = new ServerSnapshotVo.ProjectService("web", "Platform Web", host, port, status);
        when(probes.probe(any(), any(), any(), any()))
                .thenReturn(
                        new ServerSnapshotVo.ProjectService(
                                "other", "Other", null, null, "NOT_CONFIGURED"));
        when(probes.probe("web", "Platform Web", host, port)).thenReturn(web);
        var properties =
                new ServerMonitorProperties(
                        Duration.ofSeconds(30),
                        Duration.ofMillis(100),
                        Duration.ofSeconds(6),
                        "127.0.0.1",
                        0,
                        "127.0.0.1",
                        0,
                        host,
                        port,
                        false);
        var collector =
                new ServerCollector(
                        properties,
                        new MockEnvironment(),
                        Clock.systemUTC(),
                        mock(HostMetricsAdapter.class),
                        mock(JvmMetricsSampler.class),
                        mock(NvidiaGpuAdapter.class),
                        probes);

        assertThat(collector.collect().services()).contains(web);
        verify(probes, times(1)).probe("web", "Platform Web", host, port);
    }
}
