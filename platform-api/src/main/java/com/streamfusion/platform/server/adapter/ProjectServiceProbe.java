package com.streamfusion.platform.server.adapter;

import com.streamfusion.platform.server.config.ServerMonitorProperties;
import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.URI;
import java.util.concurrent.TimeUnit;
import org.springframework.stereotype.Component;

/** Only probes explicit server configuration endpoints; never scans addresses or accepts a URL. */
@Component
public class ProjectServiceProbe {
    private final ServerMonitorProperties properties;
    private final BoundedHostResolver resolver;

    public ProjectServiceProbe(ServerMonitorProperties properties, BoundedHostResolver resolver) {
        this.properties = properties;
        this.resolver = resolver;
    }

    public ServerSnapshotVo.ProjectService probe(
            String key, String name, String host, Integer port) {
        if (Thread.currentThread().isInterrupted())
            return result(key, name, host, port, "UNAVAILABLE");
        if (host == null || host.isBlank() || port == null || port <= 0 || port > 65535)
            return result(key, name, null, null, "NOT_CONFIGURED");
        long started = System.nanoTime();
        var address = resolver.resolve(host, properties.probeTimeout());
        if (address == null || Thread.currentThread().isInterrupted())
            return result(key, name, host, port, "UNAVAILABLE");
        long remaining = properties.probeTimeout().toNanos() - (System.nanoTime() - started);
        if (remaining <= 0) return result(key, name, host, port, "UNAVAILABLE");
        try (Socket socket = new Socket()) {
            socket.connect(
                    new InetSocketAddress(address, port),
                    Math.toIntExact(Math.max(1, TimeUnit.NANOSECONDS.toMillis(remaining))));
            return result(key, name, host, port, "REACHABLE");
        } catch (IOException | RuntimeException ignored) {
            return result(key, name, host, port, "UNREACHABLE");
        }
    }

    private static ServerSnapshotVo.ProjectService result(
            String key, String name, String host, Integer port, String status) {
        return new ServerSnapshotVo.ProjectService(key, name, host, port, status);
    }

    public static HostPort mysql(String url) {
        if (!url.startsWith("jdbc:mysql://")) return new HostPort(null, null);
        try {
            URI uri = URI.create(url.substring("jdbc:".length()));
            return new HostPort(uri.getHost(), uri.getPort() < 0 ? 3306 : uri.getPort());
        } catch (IllegalArgumentException ignored) {
            return new HostPort(null, null);
        }
    }

    public record HostPort(String host, Integer port) {}
}
