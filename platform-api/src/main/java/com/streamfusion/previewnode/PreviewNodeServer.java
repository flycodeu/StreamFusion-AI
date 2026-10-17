package com.streamfusion.previewnode;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import com.streamfusion.platform.media.preview.control.PreviewControlTls;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpsConfigurator;
import com.sun.net.httpserver.HttpsServer;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.util.Map;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.SSLContext;
import org.slf4j.MDC;

/** Explicit HTTPS surface: no Spring scan, datasource, Redis, sessions or business endpoints. */
public final class PreviewNodeServer implements AutoCloseable {
    private final HttpsServer server;
    private final ThreadPoolExecutor workers;
    private final PreviewNodeService service;
    private final ObjectMapper json;
    private final Path tokenFile;

    public PreviewNodeServer(
            InetSocketAddress address,
            SSLContext tls,
            Path tokenFile,
            PreviewNodeService service,
            ObjectMapper json)
            throws IOException {
        this.service = service;
        this.json = json;
        this.tokenFile = tokenFile;
        // JDK HTTP server request/response limits apply to slow and incomplete requests too.
        System.setProperty("sun.net.httpserver.maxReqTime", "20");
        System.setProperty("sun.net.httpserver.maxRspTime", "20");
        System.setProperty("sun.net.httpserver.idleInterval", "10");
        System.setProperty("jdk.httpserver.maxConnections", "128");
        System.setProperty("sun.net.httpserver.maxReqHeaders", "32");
        server = HttpsServer.create(address, 32);
        server.setHttpsConfigurator(new HttpsConfigurator(tls));
        workers =
                new ThreadPoolExecutor(
                        8,
                        8,
                        0,
                        TimeUnit.SECONDS,
                        new ArrayBlockingQueue<>(32),
                        r -> new Thread(r, "preview-control"),
                        new ThreadPoolExecutor.AbortPolicy());
        server.setExecutor(workers);
        server.createContext("/preview-control/v1/", this::handle);
    }

    public void start() {
        server.start();
    }

    public int port() {
        return server.getAddress().getPort();
    }

    private void handle(HttpExchange exchange) throws IOException {
        try {
            MDC.put("traceId", java.util.UUID.randomUUID().toString().replace("-", ""));
            String credential = exchange.getRequestHeaders().getFirst("Authorization");
            String expected = "Bearer " + PreviewControlTls.secret(tokenFile);
            if (credential == null
                    || credential.length() > 300
                    || !MessageDigest.isEqual(
                            expected.getBytes(StandardCharsets.UTF_8),
                            credential.getBytes(StandardCharsets.UTF_8))) {
                send(exchange, 401, Map.of("reasonCode", "NODE_UNAUTHORIZED"));
                return;
            }
            if (exchange.getRequestURI().getRawQuery() != null)
                throw new PreviewFailure("COMMAND_INVALID");
            String path =
                    exchange.getRequestURI().getPath().substring("/preview-control/v1/".length());
            Object result;
            if (exchange.getRequestMethod().equals("GET")) {
                if (path.equals("snapshot")) result = service.snapshot();
                else if (path.equals("maintenance")) result = service.maintenanceState();
                else if (path.startsWith("operations/")) result = service.query(path.substring(11));
                else if (path.startsWith("resources/") && path.endsWith("/stats"))
                    result = service.stats(path.substring(10, path.length() - 6));
                else if (path.startsWith("resources/"))
                    result = service.resource(path.substring(10));
                else {
                    send(exchange, 404, Map.of("reasonCode", "NOT_FOUND"));
                    return;
                }
            } else if (exchange.getRequestMethod().equals("POST") && path.equals("maintenance")) {
                var bytes = exchange.getRequestBody().readNBytes(8193);
                if (bytes.length > 8192) throw new PreviewFailure("COMMAND_TOO_LARGE");
                result =
                        service.maintain(
                                json.readValue(
                                        bytes,
                                        com.streamfusion.platform.media.preview.control
                                                .PreviewMaintenance.Command.class));
            } else if (exchange.getRequestMethod().equals("POST") && path.equals("operations")) {
                var bytes = exchange.getRequestBody().readNBytes(PreviewControl.MAX_BODY + 1);
                if (bytes.length > PreviewControl.MAX_BODY) {
                    send(exchange, 413, Map.of("reasonCode", "COMMAND_TOO_LARGE"));
                    return;
                }
                PreviewControl.Command command;
                try {
                    command = json.readValue(bytes, PreviewControl.Command.class);
                } catch (Exception invalid) {
                    throw new PreviewFailure("COMMAND_INVALID");
                }
                if (command != null
                        && command.traceId() != null
                        && command.traceId().matches("[A-Za-z0-9_-]{1,64}"))
                    MDC.put("traceId", command.traceId());
                result = service.execute(command);
            } else {
                send(exchange, 404, Map.of("reasonCode", "NOT_FOUND"));
                return;
            }
            send(exchange, 200, result);
        } catch (PreviewFailure failure) {
            // Exchange may already be closed on a disconnected client; ownership is unaffected.
            safeError(exchange, failure.reason());
        } catch (Exception failure) {
            safeError(exchange, "NODE_UNAVAILABLE");
        } finally {
            MDC.remove("traceId");
            exchange.close();
        }
    }

    private void safeError(HttpExchange exchange, String reason) {
        try {
            send(exchange, 409, Map.of("reasonCode", reason));
        } catch (IOException ignored) {
        } finally {
            exchange.close();
        }
    }

    private void send(HttpExchange exchange, int status, Object value) throws IOException {
        var bytes = json.writeValueAsBytes(value);
        exchange.getResponseHeaders().set("Content-Type", "application/json; charset=utf-8");
        exchange.getResponseHeaders().set("Cache-Control", "no-store");
        exchange.sendResponseHeaders(status, bytes.length);
        exchange.getResponseBody().write(bytes);
    }

    @Override
    public void close() {
        server.stop(1);
        workers.shutdownNow();
    }
}
