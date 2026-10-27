package com.streamfusion.platform.camera.ingress;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.execution.PreviewRuntimeProvider;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import java.io.IOException;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.stereotype.Service;

/**
 * Signal/control only. Native RTP and passive RTSP converge to the same existing RTSP/ZLM preview
 * lifecycle. A receiver stays while any ZLM reader exists, then BYE and exact cleanup.
 */
@Service
public final class CameraIngressService implements AutoCloseable {
    private final IngressProperties config;
    private final Gb28181Server sip;
    private final IngressZlmClient zlm;
    private final PreviewRuntimeProvider runtimes;
    private final GbRtpJournal journal;
    private final ObjectMapper json;
    private final Map<String, Session> sessions = new ConcurrentHashMap<>();
    private final String app =
            "sf_gb_" + UUID.randomUUID().toString().replace("-", "").substring(0, 12);
    private final ScheduledThreadPoolExecutor maintenance =
            new ScheduledThreadPoolExecutor(
                    1,
                    r -> {
                        var t = new Thread(r, "camera-ingress-maintenance");
                        t.setDaemon(true);
                        return t;
                    });
    private final ThreadPoolExecutor hooks =
            new ThreadPoolExecutor(
                    1,
                    4,
                    30,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(16),
                    r -> {
                        var t = new Thread(r, "camera-ingress-hook");
                        t.setDaemon(true);
                        return t;
                    },
                    new ThreadPoolExecutor.AbortPolicy());
    private HttpServer server;
    private volatile boolean closed;
    private int reaperFailures;

    public CameraIngressService(
            IngressProperties config,
            Gb28181Server sip,
            IngressZlmClient zlm,
            PreviewRuntimeProvider runtimes,
            GbRtpJournal journal,
            ObjectMapper json) {
        this.config = config;
        this.sip = sip;
        this.zlm = zlm;
        this.runtimes = runtimes;
        this.journal = journal;
        this.json = json;
        maintenance.setRemoveOnCancelPolicy(true);
    }

    @PostConstruct
    public void start() {
        config.validate();
        if (!config.isEnabled()) return;
        try {
            server =
                    HttpServer.create(new InetSocketAddress("127.0.0.1", config.getHookPort()), 16);
            server.createContext("/publish/", this::publish);
            server.setExecutor(hooks);
            server.start();
            maintenance.scheduleWithFixedDelay(this::reap, 5, 5, TimeUnit.SECONDS);
        } catch (IOException failure) {
            throw new IllegalStateException("INGRESS_HOOK_BIND_FAILED");
        }
    }

    public Map<String, Object> status() {
        return Map.of(
                "enabled",
                config.isEnabled(),
                "gbEnabled",
                config.isEnabled() && sip.ready(),
                "mediaHost",
                config.getMediaHost(),
                "rtspPort",
                config.getRtspPort(),
                "sipPort",
                config.getSipPort(),
                "registeredDevices",
                sip.registeredCount(),
                "activeGbSessions",
                sessions.size(),
                "configuredPushStreams",
                config.getPublishTokens().size());
    }

    public URI resolve(String kind, URI source, Runnable authorization, PreviewDeadline deadline) {
        if (!CameraStreamProtocol.ingress(kind)) return source;
        if (!config.isEnabled() || closed) throw new PreviewFailure("INGRESS_NOT_CONFIGURED");
        if (!config.getMediaHost().equals(source.getHost())
                || (source.getPort() < 0 ? 554 : source.getPort()) != config.getRtspPort())
            throw new PreviewFailure("INGRESS_ORIGIN_REJECTED");
        authorization.run();
        deadline.check();
        ActiveZlm runtime =
                runtimes.activeZlm().orElseThrow(() -> new PreviewFailure("ZLM_NOT_READY"));
        requireConfiguration(runtime, deadline);
        if ("CRTSP".equals(kind)) {
            String id = source.getPath().substring("/sf_ingress/".length());
            if (!config.getPublishTokens().containsKey(id))
                throw new PreviewFailure("PUSH_STREAM_NOT_CONFIGURED");
            var media =
                    zlm.call(
                                    runtime,
                                    "getMediaList",
                                    Map.of(
                                            "vhost",
                                            "__defaultVhost__",
                                            "app",
                                            "sf_ingress",
                                            "stream",
                                            id,
                                            "schema",
                                            "rtsp"),
                                    deadline)
                            .path("data");
            if (!media.isArray() || media.isEmpty())
                throw new PreviewFailure("PUSH_STREAM_NOT_ONLINE");
            return source;
        }
        return gb(runtime, source, authorization, deadline);
    }

    private void requireConfiguration(ActiveZlm runtime, PreviewDeadline deadline) {
        JsonNode settings = zlm.configuration(runtime, deadline);
        if (settings.path("rtsp.port").asInt() != config.getRtspPort()
                || !"1".equals(settings.path("hook.enable").asText())
                || !("http://127.0.0.1:"
                                + config.getHookPort()
                                + "/publish/"
                                + config.getHookToken())
                        .equals(settings.path("hook.on_publish").asText()))
            throw new PreviewFailure("INGRESS_ZLM_HOOK_NOT_CONFIGURED");
    }

    private synchronized URI gb(
            ActiveZlm runtime, URI source, Runnable authorization, PreviewDeadline deadline) {
        if (!sip.ready()) throw new PreviewFailure("GB_NOT_CONFIGURED");
        reaperFailures = 0; // An explicit new request permits another bounded reconciliation cycle.
        if (sessions.values().stream().anyMatch(s -> !s.entry.matches(runtime)))
            throw new PreviewFailure("GB_RECOVERY_REQUIRED");
        recover(deadline);
        String key = source.getPath();
        var existing = sessions.get(key);
        if (existing != null && existing.closing) {
            cleanup(key, existing);
            existing = null;
        }
        if (existing != null) {
            if (!existing.entry.matches(runtime)
                    || existing.dialog != null && existing.dialog.abandoned)
                throw new PreviewFailure("GB_RECOVERY_REQUIRED");
            existing.touched = System.nanoTime();
            return rtsp(existing.entry);
        }
        if (sessions.size() >= config.getMaxSessions())
            throw new PreviewFailure("GB_SESSION_CAPACITY");
        boolean[] slots = new boolean[config.getMaxSessions()];
        for (var session : sessions.values()) slots[session.slot] = true;
        int slot = 0;
        while (slots[slot]) slot++;
        String[] parts = key.split("/");
        String id = UUID.randomUUID().toString().replace("-", "");
        var entry = GbRtpJournal.Entry.of(runtime, app, id);
        var session = new Session(entry, runtime, slot, parts[1]);
        journal.register(entry); // Before any API mutation, including an unknown timeout outcome.
        sessions.put(key, session);
        try {
            authorization.run();
            deadline.check();
            // GB live SSRC: 0 + domain digits 4..8 + four-digit session suffix.
            long ssrc =
                    Long.parseLong(
                            "0"
                                    + config.getDomain().substring(3, 8)
                                    + String.format(
                                            Locale.ROOT,
                                            "%04d",
                                            Integer.toUnsignedLong(id.hashCode()) % 10000));
            int port = config.getRtpPortStart() + slot * 2;
            var result =
                    zlm.call(
                            runtime,
                            "openRtpServer",
                            Map.of(
                                    "vhost",
                                    "__defaultVhost__",
                                    "app",
                                    app,
                                    "stream_id",
                                    id,
                                    "port",
                                    Integer.toString(port),
                                    "tcp_mode",
                                    Integer.toString(config.getRtpTcpMode()),
                                    "ssrc",
                                    Long.toString(ssrc)),
                            deadline);
            if (result.path("port").asInt(0) != port)
                throw new PreviewFailure("GB_RTP_OPEN_FAILED");
            var receivers = zlm.call(runtime, "listRtpServer", Map.of(), deadline).path("data");
            boolean owned = false;
            if (receivers.isArray())
                for (var receiver : receivers)
                    if (id.equals(receiver.path("stream_id").asText())
                            && app.equals(receiver.path("app").asText())
                            && port == receiver.path("port").asInt()) owned = true;
            if (!owned) throw new PreviewFailure("GB_ZLM_VERSION_UNSUPPORTED");
            session.dialog = sip.invite(parts[1], parts[2], port, ssrc, deadline);
            authorization.run();
            deadline.check();
            return rtsp(entry);
        } catch (RuntimeException failure) {
            session.closing = true;
            try {
                cleanup(key, session);
            } catch (RuntimeException pending) {
                log("GB_RECOVERY_REQUIRED");
            }
            throw failure;
        }
    }

    private URI rtsp(GbRtpJournal.Entry entry) {
        return URI.create(
                "rtsp://"
                        + config.getMediaHost()
                        + ":"
                        + config.getRtspPort()
                        + "/"
                        + entry.app()
                        + "/"
                        + entry.id());
    }

    private void recover(PreviewDeadline deadline) {
        Set<GbRtpJournal.Entry> active = new HashSet<>();
        for (var session : sessions.values()) active.add(session.entry);
        for (var entry : journal.entries())
            if (!active.contains(entry)) {
                ActiveZlm runtime =
                        runtimes.recoveryZlm(entry.api(), entry.config())
                                .orElseThrow(() -> new PreviewFailure("GB_RECOVERY_REQUIRED"));
                zlm.close(runtime, entry, deadline);
                journal.exited(entry);
            }
    }

    private synchronized void reap() {
        if (closed || sessions.isEmpty() || reaperFailures >= 6) return;
        try (var ignored = MDC.putCloseable("traceId", "gb-ingress-reaper")) {
            ActiveZlm runtime = sessions.values().iterator().next().runtime;
            var media =
                    zlm.call(
                                    runtime,
                                    "getMediaList",
                                    Map.of(),
                                    PreviewDeadline.after(Duration.ofSeconds(3)))
                            .path("data");
            if (!media.isArray() && !media.isNull() && !media.isMissingNode())
                throw new PreviewFailure("INGRESS_ZLM_UNAVAILABLE");
            reaperFailures = 0;
            int cleanups = 0;
            for (var item : sessions.entrySet()) {
                var session = item.getValue();
                boolean read = false;
                if (media.isArray())
                    for (var stream : media)
                        if (session.entry.app().equals(stream.path("app").asText())
                                && session.entry.id().equals(stream.path("stream").asText())
                                && stream.path("totalReaderCount").asInt() > 0) read = true;
                if (read) session.touched = System.nanoTime();
                if (session.dialog != null && session.dialog.abandoned
                        || System.nanoTime() - session.touched > TimeUnit.SECONDS.toNanos(30))
                    session.closing = true;
                if (session.closing && session.failures < 6 && cleanups++ < 2) {
                    try {
                        cleanup(item.getKey(), session);
                    } catch (RuntimeException failure) {
                        session.failures++;
                        log("GB_RECOVERY_REQUIRED");
                    }
                }
            }
        } catch (RuntimeException failure) {
            reaperFailures++;
            log("INGRESS_ZLM_UNAVAILABLE");
        }
    }

    private synchronized void cleanup(String key, Session session) {
        session.closing = true;
        if (session.dialog != null)
            try {
                sip.bye(session.dialog);
            } catch (RuntimeException signalFailure) {
                /* RTP ownership still requires exact cleanup. */
            }
        zlm.close(session.runtime, session.entry, PreviewDeadline.after(Duration.ofSeconds(4)));
        journal.exited(session.entry);
        sessions.remove(key, session);
    }

    private void publish(HttpExchange exchange) throws IOException {
        var timeout = maintenance.schedule(exchange::close, 2, TimeUnit.SECONDS);
        boolean allowed = false;
        try {
            if (!"POST".equals(exchange.getRequestMethod())
                    || !equal(
                            exchange.getRequestURI().getRawPath(),
                            "/publish/" + config.getHookToken()))
                throw new IllegalArgumentException();
            byte[] bytes = exchange.getRequestBody().readNBytes(16385);
            if (bytes.length > 16384) throw new IllegalArgumentException();
            allowed = authorizedPublish(json.readTree(bytes));
        } catch (IOException | RuntimeException failure) {
            /* deny closed; never log hook tokens or source params */
        } finally {
            timeout.cancel(false);
        }
        byte[] body =
                (allowed
                                ? "{\"code\":0,\"enable_rtsp\":true,\"enable_hls\":false,\"enable_mp4\":false}"
                                : "{\"code\":-1,\"msg\":\"PUBLISH_REJECTED\"}")
                        .getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, body.length);
        try (var output = exchange.getResponseBody()) {
            output.write(body);
        } finally {
            exchange.close();
        }
    }

    boolean authorizedPublish(JsonNode value) {
        if (value == null
                || !value.isObject()
                || !"__defaultVhost__".equals(value.path("vhost").asText("__defaultVhost__")))
            return false;
        String appName = value.path("app").asText(),
                id = value.path("stream").asText(),
                ip = value.path("ip").asText();
        if ("sf_ingress".equals(appName) && "rtsp".equals(value.path("schema").asText())) {
            String expected = config.getPublishTokens().get(id);
            String supplied = null;
            for (String pair : value.path("params").asText().split("&")) {
                if (pair.startsWith("sf_token=")) {
                    if (supplied != null) return false;
                    supplied = pair.substring(9);
                }
            }
            return expected != null && equal(expected, supplied);
        }
        if (appName.startsWith("sf_preview_")
                && appName.matches("sf_preview_[a-f0-9]{12}")
                && id.matches("[a-f0-9]{32}(?:h264)?"))
            return Set.of("127.0.0.1", "::1").contains(ip);
        if (app.equals(appName))
            for (var session : sessions.values())
                if (!session.closing && session.entry.id().equals(id)) {
                    var device = config.getDevices().get(session.deviceId);
                    return device != null && device.getRemoteIps().contains(ip);
                }
        return false;
    }

    private static boolean equal(String a, String b) {
        return a != null
                && b != null
                && MessageDigest.isEqual(
                        a.getBytes(StandardCharsets.UTF_8), b.getBytes(StandardCharsets.UTF_8));
    }

    private static void log(String reason) {
        String trace = MDC.get("traceId");
        try (var ignored =
                MDC.putCloseable("traceId", trace == null ? "gb-ingress-cleanup" : trace)) {
            LoggerFactory.getLogger(CameraIngressService.class)
                    .warn("Camera ingress reconciliation pending reasonCode={}", reason);
        }
    }

    private static final class Session {
        final GbRtpJournal.Entry entry;
        final ActiveZlm runtime;
        final int slot;
        final String deviceId;
        volatile long touched = System.nanoTime();
        volatile boolean closing;
        volatile int failures;
        volatile Gb28181Server.Dialog dialog;

        Session(GbRtpJournal.Entry entry, ActiveZlm runtime, int slot, String deviceId) {
            this.entry = entry;
            this.runtime = runtime;
            this.slot = slot;
            this.deviceId = deviceId;
        }
    }

    @PreDestroy
    public synchronized void close() {
        closed = true;
        maintenance.shutdownNow();
        if (server != null) server.stop(0);
        hooks.shutdownNow();
        int attempted = 0;
        for (var entry : List.copyOf(sessions.entrySet())) {
            if (attempted++ >= 2)
                break; // Further receivers remain journalled for exact startup recovery.
            try {
                cleanup(entry.getKey(), entry.getValue());
            } catch (RuntimeException pending) {
                log("GB_RECOVERY_REQUIRED");
            }
        }
    }
}
