package com.streamfusion.platform.media.runtime.service;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.runtime.config.MediaRuntimeProperties;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Candidate;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Diagnostic;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Port;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.Flow;
import java.util.concurrent.TimeUnit;

/** Fixed FFmpeg version command and authenticated ZLM read APIs, with bounded input and output. */
final class RuntimeProbe {
    private static final int MAX_CONFIG_BYTES = 262144;
    private static final int MAX_OUTPUT_BYTES = 32768;
    private final MediaRuntimeProperties properties;
    private final ObjectMapper mapper;
    private final HttpClient http;

    RuntimeProbe(MediaRuntimeProperties properties, ObjectMapper mapper, HttpClient http) {
        this.properties = properties;
        this.mapper = mapper;
        this.http = http;
    }

    Probed inspect(RuntimeDiscovery.Discovered found, Instant checkedAt, long deadline) {
        String id = candidateId(found.kind(), found.executable(), found.config());
        if (RuntimeDiscovery.expired(deadline))
            return result(
                    found,
                    id,
                    null,
                    null,
                    "UNAVAILABLE",
                    List.of(),
                    checkedAt,
                    new Diagnostic("DETECTION_TIMEOUT", "检测超时，请重新检测"),
                    null);
        if (found.kind().equals("FFMPEG")) {
            String version = ffmpegVersion(found.executable(), deadline);
            return result(
                    found,
                    id,
                    null,
                    version,
                    version == null ? "UNAVAILABLE" : "READY",
                    List.of(),
                    checkedAt,
                    version == null
                            ? new Diagnostic("VERSION_FAILED", "无法读取版本，请检查可执行权限或重新检测")
                            : null,
                    null);
        }
        ZlmConfig config;
        try {
            if (found.config() == null)
                return result(
                        found,
                        id,
                        null,
                        null,
                        "CONFIG_MISSING",
                        List.of(),
                        checkedAt,
                        new Diagnostic("CONFIG_NOT_FOUND", "未找到配置文件，请指定当前环境的配置位置"),
                        null);
            config = readZlm(found.config());
        } catch (IOException | RuntimeException ignored) {
            return result(
                    found,
                    id,
                    null,
                    null,
                    "PROBE_FAILED",
                    List.of(),
                    checkedAt,
                    new Diagnostic("CONFIG_READ_FAILED", "无法读取配置，请检查文件权限和格式"),
                    null);
        }
        List<Port> ports = new ArrayList<>();
        for (Port port : config.ports()) {
            Boolean reachable =
                    port.protocol().endsWith("UDP") || RuntimeDiscovery.expired(deadline)
                            ? null
                            : tcpReachable(port.port(), deadline);
            ports.add(new Port(port.protocol(), port.port(), reachable));
        }
        ApiResult api =
                config.apiBase() == null
                        ? new ApiResult(null, false, "API_DISABLED")
                        : apiVersion(config, deadline);
        boolean apiReachable =
                ports.stream()
                        .anyMatch(
                                port ->
                                        port.protocol().equals("HTTP")
                                                && Boolean.TRUE.equals(port.reachable()));
        String status =
                api.ready()
                        ? found.configurationMatched() ? "READY" : "API_AVAILABLE"
                        : found.pids().isEmpty() && !apiReachable ? "STOPPED" : "PROBE_FAILED";
        Diagnostic diagnostic =
                api.ready()
                        ? found.configurationMatched()
                                ? null
                                : new Diagnostic(
                                        "PROCESS_UNCONFIRMED", "API可用，但尚未确认当前进程使用此安装和配置，请检查进程读取权限")
                        : new Diagnostic(
                                api.diagnostic(),
                                switch (api.diagnostic()) {
                                    case "API_AUTH_FAILED" -> "API鉴权未通过，请检查当前ZLM配置与运行实例是否一致";
                                    case "API_DISABLED" -> "配置未启用HTTP API";
                                    case "API_INVALID" -> "API未返回有效ZLM版本信息";
                                    default -> "API暂不可达，请检查运行状态和监听地址";
                                });
        return result(
                found,
                id,
                config.apiBase(),
                api.version(),
                status,
                ports,
                checkedAt,
                diagnostic,
                config);
    }

    private Probed result(
            RuntimeDiscovery.Discovered found,
            String id,
            URI api,
            String version,
            String status,
            List<Port> ports,
            Instant checkedAt,
            Diagnostic diagnostic,
            ZlmConfig config) {
        Candidate candidate =
                new Candidate(
                        id,
                        found.kind(),
                        found.executable().toString(),
                        found.config() == null ? null : found.config().toString(),
                        api == null ? null : api.toString(),
                        version,
                        status,
                        !found.pids().isEmpty(),
                        found.pids(),
                        List.copyOf(ports),
                        checkedAt,
                        diagnostic);
        return new Probed(candidate, config);
    }

    ZlmConfig readZlm(Path config) throws IOException {
        if (Files.size(config) > MAX_CONFIG_BYTES) throw new IOException("Config exceeds limit");
        byte[] bytes;
        try (var input = Files.newInputStream(config)) {
            bytes = input.readNBytes(MAX_CONFIG_BYTES + 1);
        }
        if (bytes.length > MAX_CONFIG_BYTES) throw new IOException("Config exceeds limit");
        Map<String, String> values = parseIni(new String(bytes, StandardCharsets.UTF_8));
        List<Port> ports = new ArrayList<>();
        addPort(ports, "HTTP", values, "http.port", 80);
        addPort(ports, "HTTPS", values, "http.sslport", 0);
        addPort(ports, "RTSP", values, "rtsp.port", 554);
        addPort(ports, "RTMP", values, "rtmp.port", 1935);
        addPort(ports, "WEBRTC_UDP", values, "rtc.port", 8000);
        addPort(
                ports,
                "WEBRTC_TCP",
                values,
                values.containsKey("rtc.tcpport") ? "rtc.tcpport" : "rtc.tcp.port",
                8000);
        int httpPort = number(values, "http.port", 80);
        // Local inventory only probes loopback, even when ZLM listens on all network interfaces.
        URI api = httpPort > 0 ? URI.create("http://127.0.0.1:" + httpPort) : null;
        return new ZlmConfig(
                api, values.getOrDefault("api.secret", ""), ports, values.get("ffmpeg.bin"));
    }

    static Map<String, String> parseIni(String input) {
        Map<String, String> values = new HashMap<>();
        String section = "";
        for (String raw : input.split("\\R")) {
            String line = raw.strip().replace("\uFEFF", "");
            if (line.isEmpty() || line.startsWith(";") || line.startsWith("#")) continue;
            if (line.startsWith("[") && line.endsWith("]")) {
                section = line.substring(1, line.length() - 1).strip().toLowerCase(Locale.ROOT);
                continue;
            }
            int split = line.indexOf('=');
            if (split <= 0) continue;
            String key = line.substring(0, split).strip().toLowerCase(Locale.ROOT);
            values.put(
                    section.isEmpty() ? key : section + "." + key,
                    line.substring(split + 1).strip());
        }
        return values;
    }

    private ApiResult apiVersion(ZlmConfig config, long deadline) {
        if (RuntimeDiscovery.expired(deadline))
            return new ApiResult(null, false, "API_UNREACHABLE");
        String query =
                config.secret().isBlank()
                        ? ""
                        : "?secret=" + URLEncoder.encode(config.secret(), StandardCharsets.UTF_8);
        java.util.concurrent.CompletableFuture<HttpResponse<byte[]>> future = null;
        try {
            HttpRequest request =
                    HttpRequest.newBuilder(
                                    URI.create(config.apiBase() + "/index/api/version" + query))
                            .timeout(java.time.Duration.ofMillis(remainingMillis(deadline)))
                            .GET()
                            .build();
            future = http.sendAsync(request, ignored -> new LimitedBody());
            var response = future.get(remainingMillis(deadline), TimeUnit.MILLISECONDS);
            if (response.statusCode() != 200 || response.body().length > MAX_OUTPUT_BYTES)
                return new ApiResult(null, false, "API_INVALID");
            JsonNode root = mapper.readTree(response.body());
            if (!root.path("code").isIntegralNumber())
                return new ApiResult(null, false, "API_INVALID");
            if (root.path("code").asInt() != 0)
                return new ApiResult(
                        null,
                        false,
                        root.path("code").asInt() == -100 ? "API_AUTH_FAILED" : "API_INVALID");
            JsonNode data = root.path("data");
            String commit = data.path("commitHash").asText("");
            String build = data.path("buildTime").asText("");
            String version = data.path("version").asText("");
            if (version.isEmpty()) version = commit + (build.isEmpty() ? "" : " (" + build + ")");
            if (version.isBlank()
                    || version.length() > 256
                    || version.codePoints().anyMatch(Character::isISOControl))
                return new ApiResult(null, false, "API_INVALID");
            return new ApiResult(version, true, null);
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            return new ApiResult(null, false, "API_UNREACHABLE");
        } catch (IOException
                | java.util.concurrent.ExecutionException
                | java.util.concurrent.TimeoutException
                | RuntimeException ignored) {
            return new ApiResult(null, false, "API_UNREACHABLE");
        } finally {
            if (future != null) future.cancel(true);
        }
    }

    private boolean tcpReachable(int port, long deadline) {
        try (Socket socket = new Socket()) {
            socket.connect(
                    new InetSocketAddress("127.0.0.1", port),
                    (int) Math.min(150, remainingMillis(deadline)));
            return true;
        } catch (IOException | RuntimeException ignored) {
            return false;
        }
    }

    private String ffmpegVersion(Path executable, long deadline) {
        // Executable is an already discovered, fixed-name binary. No shell or HTTP path is
        // accepted.
        if (!Files.isRegularFile(executable)) return null;
        Process process = null;
        try {
            process =
                    new ProcessBuilder(executable.toString(), "-version")
                            .redirectErrorStream(true)
                            .start();
            long commandDeadline =
                    System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(remainingMillis(deadline));
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            byte[] buffer = new byte[1024];
            var input = process.getInputStream();
            boolean exited = false;
            while (!RuntimeDiscovery.expired(commandDeadline)) {
                int available = input.available();
                if (available > 0) {
                    int read = input.read(buffer, 0, Math.min(buffer.length, available));
                    if (read > 0) bytes.write(buffer, 0, read);
                    if (bytes.size() > MAX_OUTPUT_BYTES) return null;
                    continue;
                }
                if (exited) {
                    if (process.exitValue() != 0) return null;
                    return parseFfmpegVersion(bytes.toString(StandardCharsets.UTF_8));
                }
                if (!process.isAlive()) {
                    exited = true;
                    continue;
                }
                Thread.sleep(10);
            }
            return null;
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            return null;
        } catch (IOException | RuntimeException ignored) {
            return null;
        } finally {
            if (process != null) {
                if (process.isAlive()) process.destroyForcibly();
                try {
                    process.getInputStream().close();
                    process.getOutputStream().close();
                    process.getErrorStream().close();
                } catch (IOException ignored) {
                }
            }
        }
    }

    static String parseFfmpegVersion(String output) {
        String first = output.lines().findFirst().orElse("").strip();
        if (!first.startsWith("ffmpeg version ") || first.length() > 256) return null;
        String version = first.substring(15).split("\\s", 2)[0];
        return version.matches("[A-Za-z0-9._+/-]{1,128}") ? version : null;
    }

    private long remainingMillis(long deadline) {
        return Math.max(
                1,
                Math.min(
                        properties.probeTimeout().toMillis(),
                        TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime())));
    }

    static String candidateId(String kind, Path executable, Path config) {
        try {
            String identity = kind + "\n" + executable + "\n" + (config == null ? "" : config);
            byte[] bytes =
                    MessageDigest.getInstance("SHA-256")
                            .digest(identity.getBytes(StandardCharsets.UTF_8));
            return kind.toLowerCase(Locale.ROOT) + "-" + HexFormat.of().formatHex(bytes, 0, 12);
        } catch (java.security.NoSuchAlgorithmException impossible) {
            throw new IllegalStateException("SHA-256 unavailable");
        }
    }

    private static void addPort(
            List<Port> ports,
            String protocol,
            Map<String, String> values,
            String key,
            int fallback) {
        int port = number(values, key, fallback);
        if (port > 0) ports.add(new Port(protocol, port, null));
    }

    private static int number(Map<String, String> values, String key, int fallback) {
        int value = Integer.parseInt(values.getOrDefault(key, Integer.toString(fallback)));
        if (value < 0 || value > 65535)
            throw new IllegalArgumentException("Invalid configured port");
        return value;
    }

    record Probed(Candidate candidate, ZlmConfig zlm) {}

    record ZlmConfig(URI apiBase, String secret, List<Port> ports, String ffmpegBin) {
        @Override
        public String toString() {
            return "ZlmConfig[apiBase=" + apiBase + ", secret=<redacted>]";
        }
    }

    private record ApiResult(String version, boolean ready, String diagnostic) {}

    private static final class LimitedBody implements HttpResponse.BodySubscriber<byte[]> {
        private final CompletableFuture<byte[]> result = new CompletableFuture<>();
        private final ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        private Flow.Subscription subscription;

        @Override
        public CompletionStage<byte[]> getBody() {
            return result;
        }

        @Override
        public void onSubscribe(Flow.Subscription value) {
            subscription = value;
            subscription.request(1);
        }

        @Override
        public void onNext(List<ByteBuffer> buffers) {
            for (ByteBuffer buffer : buffers) {
                if (bytes.size() + buffer.remaining() > MAX_OUTPUT_BYTES) {
                    subscription.cancel();
                    result.completeExceptionally(
                            new IOException("Media API response exceeds limit"));
                    return;
                }
                byte[] chunk = new byte[buffer.remaining()];
                buffer.get(chunk);
                bytes.writeBytes(chunk);
            }
            subscription.request(1);
        }

        @Override
        public void onError(Throwable failure) {
            result.completeExceptionally(failure);
        }

        @Override
        public void onComplete() {
            result.complete(bytes.toByteArray());
        }
    }
}
