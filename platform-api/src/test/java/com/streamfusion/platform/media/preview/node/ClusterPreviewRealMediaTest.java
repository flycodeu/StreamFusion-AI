package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor.SourceKey;
import com.streamfusion.platform.support.IdentitySchema;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.springframework.jdbc.core.JdbcTemplate;

/** Opt-in local media plus two separately launched PreviewNode JVMs. No business services. */
@EnabledIfEnvironmentVariable(named = "SF_PREVIEW_CLUSTER_BROWSER_TEST", matches = "true")
class ClusterPreviewRealMediaTest {
    private final ObjectMapper json = new ObjectMapper().findAndRegisterModules();

    private PreviewDeadline budget() {
        return PreviewDeadline.after(Duration.ofSeconds(18));
    }

    @Test
    void browserPlaysLocalAndTwoProcessesWithSharedRemoteTranscode() throws Exception {
        Path input = Path.of(System.getenv("SF_PREVIEW_CLUSTER_CONFIG"));
        var config = json.readTree(input.toFile());
        Path evidence = Path.of(config.path("evidence").asText());
        Files.createDirectories(evidence);
        var localConfig =
                com.streamfusion.previewnode.PreviewNodeConfig.read(
                        Path.of(config.path("localConfig").asText()), json);
        var pulls =
                new com.streamfusion.platform.media.preview.media.PreviewPullRegistry(
                        localConfig.workDir(), json);
        var transcodes =
                new com.streamfusion.platform.media.preview.media.PreviewTranscodeRegistry(
                        localConfig.workDir(), json, 64);
        var gateway =
                new com.streamfusion.platform.media.preview.media.ZlmPreviewGateway(
                        json,
                        localConfig,
                        transcodes,
                        new com.streamfusion.platform.media.preview.media
                                .PreviewProcessInspector());
        var local =
                new com.streamfusion.platform.media.preview.execution.LocalPreviewExecutor(
                        gateway,
                        localConfig,
                        pulls,
                        transcodes,
                        new com.streamfusion.platform.media.preview.execution
                                .PreviewRecoveryCoordinator(pulls, gateway, localConfig));
        local.reconcile(() -> false);
        var data = new JdbcDataSource();
        data.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(data);
        var sql = new JdbcTemplate(data);
        var repository = new PreviewNodeRepository(sql);
        var manager =
                new PreviewNodeManager(
                        repository,
                        new PreviewNodeProperties(config.path("connections").asText(), ""),
                        new PreviewProperties(64, 64),
                        json);
        for (String nodeId : List.of("a", "b")) {
            manager.register(manager.registration(nodeId));
        }
        var small =
                new com.streamfusion.platform.media.preview.control.PreviewBudget(
                        1, 64, 64, 4, 655360, 655360, 8192, 8192);
        for (String nodeId : List.of("local", "a", "b")) {
            var node = repository.node(nodeId);
            manager.policy(
                    manager.checkPolicy(
                            nodeId,
                            new PreviewNodeManager.Policy(
                                    "REMOTE",
                                    nodeId.equals("local") ? 0 : 100,
                                    small,
                                    node.version())));
        }
        var ledger =
                new PreviewReservationLedger(
                        sql,
                        repository,
                        new org.springframework.jdbc.datasource.DataSourceTransactionManager(data));
        var scheduler = new PreviewScheduler(repository, ledger, manager, local);
        var executor =
                new com.streamfusion.platform.media.preview.execution.PreviewRoutingExecutor(
                        local,
                        new RemotePreviewExecutor(repository, manager, json),
                        manager,
                        repository,
                        scheduler);
        var finished = new CompletableFuture<JsonNode>();
        var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 8);
        var workers = Executors.newFixedThreadPool(4);
        server.setExecutor(workers);
        server.createContext(
                "/",
                exchange -> {
                    try {
                        String path = exchange.getRequestURI().getPath();
                        byte[] response;
                        if (path.equals("/") && exchange.getRequestMethod().equals("GET")) {
                            exchange.getResponseHeaders()
                                    .set("Content-Type", "text/html; charset=utf-8");
                            response = page().getBytes(StandardCharsets.UTF_8);
                        } else if (exchange.getRequestMethod().equals("POST")) {
                            String body =
                                    new String(
                                            exchange.getRequestBody().readNBytes(65537),
                                            StandardCharsets.UTF_8);
                            if (body.length() > 65536)
                                throw new IllegalArgumentException("Oversized harness input");
                            Object value;
                            if (path.startsWith("/start/")) {
                                int slot = Integer.parseInt(path.substring(7));
                                String codec = slot == 2 || slot == 4 ? "h265" : "h264";
                                long profile = slot == 4 ? 2 : slot;
                                var key = new SourceKey(profile, "fixture-v1");
                                String id = UUID.randomUUID().toString();
                                executor.prepare(id, key, List.of("H264"), budget());
                                executor.attachSource(
                                        id,
                                        key,
                                        () ->
                                                URI.create(
                                                        "rtsp://127.0.0.1:"
                                                                + config.path("rtspPort").asInt()
                                                                + "/fixture/"
                                                                + codec),
                                        () -> {},
                                        budget());
                                var playback =
                                        executor.start(id, List.of("H264"), () -> {}, budget());
                                executor.confirmReady(id, budget());
                                value =
                                        java.util.Map.of(
                                                "id",
                                                id,
                                                "codec",
                                                playback.codec(),
                                                "transcoded",
                                                playback.transcoded(),
                                                "nodeId",
                                                repository.viewer(id).nodeId());
                            } else if (path.startsWith("/offer/"))
                                value = executor.answer(path.substring(7), body, budget());
                            else if (path.startsWith("/renew/")) {
                                executor.renew(path.substring(7), budget());
                                value = java.util.Map.of("renewed", true);
                            } else if (path.startsWith("/stop/")) {
                                executor.close(path.substring(6), budget());
                                value = java.util.Map.of("closed", true);
                            } else if (path.equals("/state")) {
                                var usage =
                                        java.util.Map.of(
                                                "local",
                                                ledger.usage("local"),
                                                "a",
                                                ledger.usage("a"),
                                                "b",
                                                ledger.usage("b"));
                                assertThat(ledger.usage("a").sources()).isOne();
                                assertThat(ledger.usage("a").transcodes()).isOne();
                                assertThat(ledger.usage("a").viewers()).isEqualTo(2);
                                json.writeValue(
                                        evidence.resolve("active-ledger.json").toFile(), usage);
                                value = usage;
                            } else if (path.equals("/result")) {
                                finished.complete(json.readTree(body));
                                value = java.util.Map.of("recorded", true);
                            } else throw new IllegalArgumentException("Unknown harness route");
                            exchange.getResponseHeaders().set("Content-Type", "application/json");
                            response = json.writeValueAsBytes(value);
                        } else {
                            exchange.sendResponseHeaders(404, -1);
                            return;
                        }
                        exchange.sendResponseHeaders(200, response.length);
                        exchange.getResponseBody().write(response);
                    } catch (Exception failure) {
                        var bytes =
                                json.writeValueAsBytes(
                                        java.util.Map.of(
                                                "error",
                                                failure
                                                                instanceof
                                                                com.streamfusion.platform.media
                                                                                .preview
                                                                                .PreviewFailure
                                                                        known
                                                        ? known.reason()
                                                        : "HARNESS_FAILED"));
                        exchange.sendResponseHeaders(500, bytes.length);
                        exchange.getResponseBody().write(bytes);
                    } finally {
                        exchange.close();
                    }
                });
        try {
            server.start();
            json.writeValue(
                    evidence.resolve("browser-harness.json").toFile(),
                    java.util.Map.of(
                            "url",
                            "http://127.0.0.1:" + server.getAddress().getPort(),
                            "nodes",
                            List.of("local", "a", "b")));
            var result = finished.get(240, TimeUnit.SECONDS);
            json.writeValue(evidence.resolve("browser-result.json").toFile(), result);
            assertThat(result.path("error").isMissingNode()).isTrue();
            assertThat(result.path("cases")).hasSize(4);
            for (var item : result.path("cases")) {
                assertThat(item.path("width").asInt()).isEqualTo(640);
                assertThat(item.path("height").asInt()).isEqualTo(360);
                assertThat(item.path("frames").asInt()).isGreaterThan(10);
                assertThat(item.path("bytesReceived").asLong()).isPositive();
                assertThat(item.path("transcoded").asBoolean())
                        .isEqualTo(item.path("source").asText().equals("h265"));
            }
            assertThat(repository.viewers()).isEmpty();
            for (String nodeId : List.of("a", "b")) {
                var snapshot = manager.inspect(repository.node(nodeId), budget());
                assertThat(snapshot.resources()).isEmpty();
                assertThat(snapshot.occupiedTranscodes()).isZero();
                json.writeValue(evidence.resolve(nodeId + "-after.json").toFile(), snapshot);
            }
            assertThat(local.occupiedTranscodes()).isZero();
            assertThat(sql.queryForObject("SELECT COUNT(*) FROM preview_source", Integer.class))
                    .isZero();
            assertThat(sql.queryForObject("SELECT COUNT(*) FROM preview_output", Integer.class))
                    .isZero();
        } finally {
            server.stop(0);
            workers.shutdownNow();
            for (var viewer : repository.viewers()) {
                try {
                    executor.close(viewer.id(), budget());
                } catch (Exception ignored) {
                }
            }
            pulls.close();
            transcodes.close();
        }
    }

    private String page() throws java.io.IOException {
        return new org.springframework.core.io.ClassPathResource("preview-cluster-browser.html")
                .getContentAsString(StandardCharsets.UTF_8);
    }
}
