package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.awaitility.Awaitility.await;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.doReturn;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.support.CameraTestSupport;
import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.TestPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;

/**
 * Real worker, TCP probe, SQL and API. Only loopback policy and inherited login stores are test
 * doubles.
 */
@TestPropertySource(properties = "platform.camera.status.probe-timeout-ms=1000")
class CameraStatusNetworkIntegrationTest extends CameraTestSupport {
    @Autowired CameraStatusPlanService scheduler;
    @Autowired CameraStatusCheckMapper checks;
    @Autowired CameraStatusSnapshotMapper snapshots;
    @MockitoSpyBean CameraSourceRules rules;
    @Autowired com.streamfusion.platform.camera.service.CameraCryptoService crypto;
    @Autowired com.streamfusion.platform.camera.mapper.CameraLocatorMapper locators;

    @BeforeEach
    void allowFixtureOnly() {
        // Loopback is intentionally forbidden in production. No production bypass is introduced.
        doReturn("127.0.0.1").when(rules).host(eq("127.0.0.1"), eq("test"));
    }

    @ParameterizedTest
    @CsvSource({
        "200,ACCESSIBLE,RTSP_ACCESSIBLE",
        "401,FAILED,AUTH_REJECTED",
        "404,FAILED,RTSP_PATH_NOT_FOUND",
        "0,FAILED,RTSP_TIMEOUT"
    })
    void taskRunsRealNetworkProbeAndExposesPersistedFacts(int response, String value, String reason)
            throws Exception {
        try (var fixture = new RtspFixture(response)) {
            var auth = admin();
            var camera = simulatedCamera(auth, fixture.port());
            String detectionProfileId =
                    jdbc.queryForObject(
                                    "SELECT id FROM camera_stream_profile WHERE channel_id=? AND usage_hint='SUB'",
                                    Long.class,
                                    camera.path("cameraId").asLong())
                            .toString();
            assertThat(detectionProfileId)
                    .isNotEqualTo(camera.path("defaultPreviewProfileId").asText());
            var plan = start(auth, camera);
            auth.session().invalidate();
            makeDue(plan);
            scheduler.tick();
            await().atMost(Duration.ofSeconds(5))
                    .untilAsserted(
                            () -> {
                                assertThat(checks.selectList(null)).hasSize(1);
                                assertThat(checks.selectList(null).getFirst().getExecutionState())
                                        .isEqualTo("COMPLETED");
                                assertThat(snapshots.forCamera(camera.path("cameraId").asLong()))
                                        .hasSize(1);
                            });
            assertThat(fixture.requests).hasSize(1);
            assertThat(fixture.requests.getFirst())
                    .isEqualTo(
                            "DESCRIBE rtsp://127.0.0.1:"
                                    + fixture.port()
                                    + "/private-sub RTSP/1.0");
            var fact = snapshots.forCamera(camera.path("cameraId").asLong()).getFirst();
            assertThat(fact.getValue()).isEqualTo(value);
            assertThat(fact.getReasonCode()).isEqualTo(reason);
            assertThat(fact.getStateSince()).isNotNull();
            assertThat(fact.getProfileId()).isEqualTo(Long.valueOf(detectionProfileId));
            var renewed = login("CameraAdmin", "AdminPass1!");
            var status = read("/cameras/" + camera.path("cameraId").asText() + "/status", renewed);
            var profiles = new ArrayList<JsonNode>();
            status.path("profiles").forEach(profiles::add);
            var selected =
                    profiles.stream()
                            .filter(
                                    p ->
                                            p.path("streamProfileId")
                                                    .asText()
                                                    .equals(detectionProfileId))
                            .findFirst()
                            .orElseThrow();
            assertThat(selected.path("control").path("observation").path("reasonCode").asText())
                    .isEqualTo(reason);
            assertThat(selected.path("control").path("timeline").path("stateSince").asText())
                    .isNotBlank();
            assertThat(
                            profiles.stream()
                                    .filter(p -> !p.path("control").path("observation").isNull()))
                    .hasSize(1);
            var listed = read("/camera-status/plans", renewed).get(0);
            assertThat(listed.path("progress").path(response == 200 ? "success" : "failed").asInt())
                    .isOne();
            write(
                    post("/camera-status/plans/" + plan.path("planId").asText() + "/stop"),
                    renewed,
                    Map.of("version", listed.path("version").asText()),
                    200);
            makeDue(plan);
            scheduler.tick();
            assertThat(checks.selectList(null)).hasSize(1);
        }
    }

    @Test
    void stoppedTaskCannotPublishDelayedNetworkSuccess() throws Exception {
        try (var fixture = new RtspFixture(-1)) {
            var auth = admin();
            var camera = simulatedCamera(auth, fixture.port());
            var plan = start(auth, camera);
            makeDue(plan);
            scheduler.tick();
            assertThat(fixture.received.await(3, TimeUnit.SECONDS)).isTrue();
            write(
                    post("/camera-status/plans/" + plan.path("planId").asText() + "/stop"),
                    auth,
                    Map.of("version", "0"),
                    200);
            fixture.release.countDown();
            await().atMost(Duration.ofSeconds(5))
                    .untilAsserted(
                            () ->
                                    assertThat(
                                                    checks.selectList(null)
                                                            .getFirst()
                                                            .getExecutionState())
                                            .isNotIn("QUEUED", "RUNNING"));
            assertThat(checks.selectList(null).getFirst().getSnapshotApplied()).isFalse();
            assertThat(snapshots.forCamera(camera.path("cameraId").asLong())).isEmpty();
        }
    }

    @ParameterizedTest
    @CsvSource({
        "HIKVISION,1,/Streaming/Channels/101",
        "DAHUA,1,/cam/realmonitor?channel=3&subtype=1",
        "ONVIF,1,/resolved-main",
        "ONVIF,2,/resolved-main"
    })
    void importedProtocolRunsRealRtspAfterReadOnlyQualification(
            String kind, int mediaVersion, String path) throws Exception {
        try (var rtsp = new RtspFixture(200);
                var http =
                        new OnvifFixture(
                                200, "rtsp://127.0.0.1:" + rtsp.port() + "/resolved-main")) {
            var auth = admin();
            var camera = simulatedCamera(auth, rtsp.port());
            installProtocol(camera, kind, mediaVersion, http.port(), rtsp.port());
            var status = read("/cameras/" + camera.path("cameraId").asText() + "/status", auth);
            assertThat(status.path("profiles").get(0).path("control").path("canCheck").asBoolean())
                    .isTrue();
            var plan = start(auth, camera);
            assertThat(http.requests).isEmpty();
            assertThat(rtsp.requests).isEmpty();
            makeDue(plan);
            scheduler.tick();
            await().atMost(Duration.ofSeconds(5))
                    .untilAsserted(
                            () -> {
                                assertThat(snapshots.forCamera(camera.path("cameraId").asLong()))
                                        .hasSize(1);
                                assertThat(checks.selectList(null).getFirst().getExecutionState())
                                        .isEqualTo("COMPLETED");
                            });
            assertThat(snapshots.forCamera(camera.path("cameraId").asLong()).getFirst().getValue())
                    .isEqualTo("ACCESSIBLE");
            assertThat(rtsp.requests)
                    .containsExactly(
                            "DESCRIBE rtsp://127.0.0.1:" + rtsp.port() + path + " RTSP/1.0");
            if ("ONVIF".equals(kind)) {
                assertThat(http.requests).hasSize(1);
                assertThat(http.requests.getFirst())
                        .contains("<m:ProfileToken>fixture-main</m:ProfileToken>");
                assertThat(http.requests.getFirst())
                        .contains(mediaVersion == 2 ? "ver20/media/wsdl" : "ver10/media/wsdl");
            } else assertThat(http.requests).isEmpty();
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT locator_kind FROM camera_profile_locator WHERE profile_id=?",
                                    String.class,
                                    camera.path("defaultPreviewProfileId").asLong()))
                    .isEqualTo(kind);
        }
    }

    @ParameterizedTest
    @CsvSource({
        "401,STREAM_URI_AUTH_REJECTED",
        "500,STREAM_URI_UNAVAILABLE",
        "0,STREAM_URI_TIMEOUT"
    })
    void onvifResolutionFailureDoesNotPublishOfflineFact(int response, String reason)
            throws Exception {
        try (var http = new OnvifFixture(response, "rtsp://127.0.0.1:9999/not-contacted")) {
            var auth = admin();
            var camera = simulatedCamera(auth, 9999);
            installProtocol(camera, "ONVIF", 1, http.port(), 9999);
            var plan = start(auth, camera);
            makeDue(plan);
            scheduler.tick();
            await().atMost(Duration.ofSeconds(5))
                    .untilAsserted(
                            () -> {
                                assertThat(checks.selectList(null)).hasSize(1);
                                assertThat(checks.selectList(null).getFirst().getExecutionState())
                                        .isEqualTo("SKIPPED");
                            });
            assertThat(checks.selectList(null).getFirst().getReasonCode()).isEqualTo(reason);
            assertThat(snapshots.forCamera(camera.path("cameraId").asLong())).isEmpty();
        }
    }

    @Test
    void stoppingDuringOnvifResolutionPreventsRtspAndSnapshot() throws Exception {
        try (var rtsp = new RtspFixture(200);
                var http = new OnvifFixture(-1, "rtsp://127.0.0.1:" + rtsp.port() + "/late")) {
            var auth = admin();
            var camera = simulatedCamera(auth, rtsp.port());
            installProtocol(camera, "ONVIF", 1, http.port(), rtsp.port());
            var plan = start(auth, camera);
            makeDue(plan);
            scheduler.tick();
            assertThat(http.received.await(3, TimeUnit.SECONDS)).isTrue();
            write(
                    post("/camera-status/plans/" + plan.path("planId").asText() + "/stop"),
                    auth,
                    Map.of("version", "0"),
                    200);
            http.release.countDown();
            await().atMost(Duration.ofSeconds(5))
                    .untilAsserted(
                            () ->
                                    assertThat(
                                                    checks.selectList(null)
                                                            .getFirst()
                                                            .getExecutionState())
                                            .isNotIn("QUEUED", "RUNNING"));
            assertThat(rtsp.requests).isEmpty();
            assertThat(snapshots.forCamera(camera.path("cameraId").asLong())).isEmpty();
        }
    }

    private void installProtocol(
            JsonNode camera, String kind, int mediaVersion, int httpPort, int rtspPort) {
        long sourceId = camera.path("sourceId").asLong();
        String purpose = "ONVIF".equals(kind) ? "ONVIF" : "VENDOR_HTTP";
        var imported =
                locators.selectList(
                        new LambdaQueryWrapper<CameraLocatorEntity>()
                                .eq(CameraLocatorEntity::getSourceId, sourceId));
        for (var locator : imported) locators.deleteById(locator.getProfileId());
        jdbc.update(
                "UPDATE camera_source SET adapter_type=?,rtsp_port=? WHERE id=?",
                kind,
                "ONVIF".equals(kind) ? 8554 : rtspPort,
                sourceId);
        jdbc.update(
                "UPDATE camera_source_endpoint SET purpose=?,scheme='http',port=?,base_path=? WHERE source_id=?",
                purpose,
                httpPort,
                "ONVIF".equals(kind) ? "/onvif/device_service" : "",
                sourceId);
        for (var locator : imported) {
            Map<String, Object> config =
                    switch (kind) {
                        case "ONVIF" ->
                                Map.of(
                                        "profileToken",
                                        "fixture-main",
                                        "serviceNamespace",
                                        "http://www.onvif.org/ver"
                                                + (mediaVersion == 2 ? "20" : "10")
                                                + "/media/wsdl",
                                        "serviceEndpoint",
                                        "http://127.0.0.1:" + httpPort + "/onvif/media");
                        case "HIKVISION" -> Map.of("profileToken", "101");
                        default -> Map.of("channelKey", "Encode[2]", "streamType", 1);
                    };
            var secret =
                    crypto.encrypt(
                            "camera_profile_locator/"
                                    + sourceId
                                    + "/"
                                    + locator.getProfileId()
                                    + "/"
                                    + kind,
                            config,
                            16000);
            locator.setLocatorKind(kind);
            locator.setEndpointPurpose(purpose);
            locator.setRtspHostMode(null);
            locator.setRtspHost(null);
            locator.setRtspPort(null);
            locator.setRtspTransport(null);
            locator.setRtspEncryptionKeyId(null);
            locator.setRtspSecretCiphertext(null);
            locator.setRtspSecretNonce(null);
            locator.setRtspSecretTag(null);
            locator.setProtocolEncryptionKeyId(secret.keyId());
            locator.setProtocolSecretCiphertext(secret.ciphertext());
            locator.setProtocolSecretNonce(secret.nonce());
            locator.setProtocolSecretTag(secret.tag());
            locators.insert(locator);
        }
    }

    private static final class OnvifFixture implements AutoCloseable {
        final com.sun.net.httpserver.HttpServer server;
        final java.util.concurrent.ExecutorService executor = Executors.newSingleThreadExecutor();
        final List<String> requests = new CopyOnWriteArrayList<>();
        final CountDownLatch received = new CountDownLatch(1), release = new CountDownLatch(1);

        OnvifFixture(int status, String rtsp) throws Exception {
            server =
                    com.sun.net.httpserver.HttpServer.create(
                            new java.net.InetSocketAddress("127.0.0.1", 0), 1);
            server.setExecutor(executor);
            server.createContext(
                    "/onvif/media",
                    exchange -> {
                        try (exchange) {
                            requests.add(
                                    new String(
                                            exchange.getRequestBody().readAllBytes(),
                                            StandardCharsets.UTF_8));
                            received.countDown();
                            if (status <= 0) {
                                try {
                                    release.await(3, TimeUnit.SECONDS);
                                } catch (InterruptedException ex) {
                                    Thread.currentThread().interrupt();
                                    return;
                                }
                                if (status == 0) return;
                            }
                            String xml =
                                    "<s:Envelope xmlns:s=\"http://www.w3.org/2003/05/soap-envelope\"><s:Body><GetStreamUriResponse><Uri>"
                                            + rtsp.replace("&", "&amp;")
                                            + "</Uri></GetStreamUriResponse></s:Body></s:Envelope>";
                            byte[] bytes = xml.getBytes(StandardCharsets.UTF_8);
                            exchange.sendResponseHeaders(status < 0 ? 200 : status, bytes.length);
                            exchange.getResponseBody().write(bytes);
                        }
                    });
            server.start();
        }

        int port() {
            return server.getAddress().getPort();
        }

        @Override
        public void close() throws Exception {
            release.countDown();
            server.stop(0);
            executor.shutdownNow();
            assertThat(executor.awaitTermination(5, TimeUnit.SECONDS)).isTrue();
        }
    }

    private JsonNode simulatedCamera(Csrf auth, int port) throws Exception {
        var body = new HashMap<>(sourceBody(requestId()));
        body.put("name", "模拟 RTSP · 仅测试");
        body.put(
                "endpoints",
                List.of(
                        Map.of(
                                "purpose",
                                "RTSP",
                                "scheme",
                                "rtsp",
                                "host",
                                "127.0.0.1",
                                "port",
                                port,
                                "basePath",
                                "",
                                "authMode",
                                "NONE",
                                "tlsPolicy",
                                "SYSTEM_CA")));
        var source = write(post("/camera-sources"), auth, body, 201);
        var camera = camera(auth, source.path("sourceId").asText(), "模拟相机 · 仅测试");
        var group = write(post("/camera-groups"), auth, Map.of("name", "模拟测试组"), 201);
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                group.path("groupId").asLong(),
                camera.path("cameraId").asLong());
        return camera;
    }

    private JsonNode start(Csrf auth, JsonNode camera) throws Exception {
        var preview =
                write(
                        post("/camera-status/plans/targets/preview"),
                        auth,
                        Map.of("scope", "ALL"),
                        200);
        assertThat(preview.path("targets")).hasSize(1);
        assertThat(preview.path("targets").get(0).path("reasonCode").isNull()).isTrue();
        return write(
                post("/camera-status/plans"),
                auth,
                Map.of(
                        "clientRequestId",
                        requestId(),
                        "groupKeys",
                        List.of("c" + camera.path("cameraId").asText()),
                        "intervalMinutes",
                        60,
                        "durationHours",
                        24),
                201);
    }

    private void makeDue(JsonNode plan) {
        jdbc.update(
                "UPDATE camera_status_plan SET start_at=DATEADD('SECOND',-1,CURRENT_TIMESTAMP) WHERE id=?",
                plan.path("planId").asLong());
        jdbc.update(
                "UPDATE camera_status_plan_target SET next_at=DATEADD('SECOND',-1,CURRENT_TIMESTAMP) WHERE plan_id=?",
                plan.path("planId").asLong());
    }

    private static final class RtspFixture implements AutoCloseable {
        private static final String SDP =
                "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=simulator\r\nt=0 0\r\nm=video 0 RTP/AVP 96\r\na=rtpmap:96 H264/90000\r\n";
        final List<String> requests = new CopyOnWriteArrayList<>();
        final CountDownLatch received = new CountDownLatch(1), release = new CountDownLatch(1);
        final ServerSocket server;
        final java.util.concurrent.ExecutorService executor = Executors.newSingleThreadExecutor();
        final java.util.concurrent.Future<?> serving;

        RtspFixture(int status) throws Exception {
            server = new ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"));
            server.setSoTimeout(5000);
            serving =
                    executor.submit(
                            () -> {
                                try (Socket socket = server.accept()) {
                                    socket.setSoTimeout(4000);
                                    var reader =
                                            new BufferedReader(
                                                    new InputStreamReader(
                                                            socket.getInputStream(),
                                                            StandardCharsets.US_ASCII));
                                    requests.add(reader.readLine());
                                    String line, sequence = "1";
                                    while ((line = reader.readLine()) != null && !line.isEmpty())
                                        if (line.toLowerCase(java.util.Locale.ROOT)
                                                .startsWith("cseq:"))
                                            sequence = line.substring(5).strip();
                                    received.countDown();
                                    if (status <= 0) {
                                        if (!release.await(4, TimeUnit.SECONDS) || status == 0)
                                            return;
                                    }
                                    String body = status == 200 || status == -1 ? SDP : "";
                                    String reply =
                                            "RTSP/1.0 "
                                                    + (status == -1 ? 200 : status)
                                                    + " Fixture\r\nCSeq: "
                                                    + sequence
                                                    + "\r\nContent-Type: application/sdp\r\n"
                                                    + (status == 401
                                                            ? "WWW-Authenticate: Basic realm=\"simulator\"\r\n"
                                                            : "")
                                                    + "Content-Length: "
                                                    + body.length()
                                                    + "\r\n\r\n"
                                                    + body;
                                    socket.getOutputStream()
                                            .write(reply.getBytes(StandardCharsets.US_ASCII));
                                    socket.getOutputStream().flush();
                                } catch (Exception ex) {
                                    if (server.isClosed() && ex instanceof java.net.SocketException)
                                        return;
                                    throw new IllegalStateException("RTSP fixture failed", ex);
                                }
                            });
        }

        int port() {
            return server.getLocalPort();
        }

        @Override
        public void close() throws Exception {
            release.countDown();
            server.close();
            executor.shutdown();
            assertThat(executor.awaitTermination(5, TimeUnit.SECONDS)).isTrue();
            serving.get(1, TimeUnit.SECONDS);
        }
    }
}
