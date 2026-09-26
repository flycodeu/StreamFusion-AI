package com.streamfusion.platform.server.service;

import com.streamfusion.platform.server.adapter.HostMetricsAdapter;
import com.streamfusion.platform.server.adapter.JvmMetricsSampler;
import com.streamfusion.platform.server.adapter.NvidiaGpuAdapter;
import com.streamfusion.platform.server.adapter.ProjectServiceProbe;
import com.streamfusion.platform.server.config.ServerMonitorProperties;
import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.time.Clock;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CancellationException;
import org.springframework.boot.web.context.WebServerInitializedEvent;
import org.springframework.context.event.EventListener;
import org.springframework.core.env.Environment;
import org.springframework.stereotype.Component;

/** Coordinates one bounded sample; platform details belong to the injected adapters. */
@Component
public class ServerCollector {
    private final ServerMonitorProperties properties;
    private final Environment environment;
    private final Clock clock;
    private final HostMetricsAdapter hostMetrics;
    private final JvmMetricsSampler jvmMetrics;
    private final NvidiaGpuAdapter gpuMetrics;
    private final ProjectServiceProbe serviceProbe;
    private volatile int apiPort;

    public ServerCollector(
            ServerMonitorProperties properties,
            Environment environment,
            Clock clock,
            HostMetricsAdapter hostMetrics,
            JvmMetricsSampler jvmMetrics,
            NvidiaGpuAdapter gpuMetrics,
            ProjectServiceProbe serviceProbe) {
        this.properties = properties;
        this.environment = environment;
        this.clock = clock;
        this.hostMetrics = hostMetrics;
        this.jvmMetrics = jvmMetrics;
        this.gpuMetrics = gpuMetrics;
        this.serviceProbe = serviceProbe;
        this.apiPort = environment.getProperty("server.port", Integer.class, 8080);
    }

    @EventListener
    public void serverStarted(WebServerInitializedEvent event) {
        if (event.getApplicationContext().getServerNamespace() == null)
            apiPort = event.getWebServer().getPort();
    }

    public ServerSnapshotVo collect() {
        checkInterrupted();
        var hostStart = hostMetrics.beginCpuSample();
        var jvmStart = jvmMetrics.beginCpuSample();
        try {
            Thread.sleep(HostMetricsAdapter.CPU_WINDOW);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new CancellationException("Monitoring sample interrupted");
        }
        checkInterrupted();
        var host = hostMetrics.finishCpuSample(hostStart);
        var jvm = jvmMetrics.finishCpuSample(jvmStart);
        List<ServerSnapshotVo.ProjectService> services = new ArrayList<>();
        services.add(
                new ServerSnapshotVo.ProjectService(
                        "api", "Platform API", "127.0.0.1", apiPort, "RUNNING"));
        services.add(
                serviceProbe.probe(
                        "web", "Platform Web", properties.webHost(), properties.webPort()));
        var database =
                ProjectServiceProbe.mysql(environment.getProperty("spring.datasource.url", ""));
        services.add(serviceProbe.probe("mysql", "MySQL", database.host(), database.port()));
        services.add(
                serviceProbe.probe(
                        "redis",
                        "Redis",
                        environment.getProperty("spring.data.redis.host", "127.0.0.1"),
                        environment.getProperty("spring.data.redis.port", Integer.class, 6379)));
        services.add(
                serviceProbe.probe(
                        "agent", "Node Agent", properties.agentHost(), properties.agentPort()));
        services.add(
                serviceProbe.probe(
                        "runtime",
                        "Algorithm Runtime",
                        properties.runtimeHost(),
                        properties.runtimePort()));
        checkInterrupted();
        var gpu =
                properties.gpuEnabled()
                        ? gpuMetrics.collect()
                        : new NvidiaGpuAdapter.GpuResult("DISABLED", List.of());
        checkInterrupted();
        return new ServerSnapshotVo(
                clock.instant(),
                false,
                "READY",
                host,
                jvm,
                List.copyOf(services),
                gpu.status(),
                gpu.devices());
    }

    private static void checkInterrupted() {
        if (Thread.currentThread().isInterrupted())
            throw new CancellationException("Monitoring sample interrupted");
    }
}
