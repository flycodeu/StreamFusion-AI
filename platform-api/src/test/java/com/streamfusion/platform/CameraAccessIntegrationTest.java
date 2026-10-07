package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.service.*;
import com.streamfusion.platform.support.CameraTestSupport;
import java.net.URI;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/**
 * HTTP + real SQL asset transactions; outbound discovery and Redis actor store have separate tests.
 */
class CameraAccessIntegrationTest extends CameraTestSupport {
    @MockitoBean CameraAccessWorker worker;
    @MockitoBean CameraJobActor actorCheck;
    @Autowired CameraAccessJobsService jobs;
    @Autowired CameraConnectionRules connections;

    @Test
    void fullRtspInputCreatesAssetsWithoutManualInternalProfilesAndReplays() throws Exception {
        var auth = admin();
        var body = rtsp();
        body.put("clientRequestId", requestId());
        var first = write(post("/camera-access/jobs"), auth, body, 202);
        assertThat(write(post("/camera-access/jobs"), auth, body, 202).path("jobId"))
                .isEqualTo(first.path("jobId"));
        String id = first.path("jobId").asText();
        var work = jobs.claim(Long.parseLong(id));
        assertThat(work.payload().connection().username()).isEqualTo("user");
        assertThat(work.payload().connection().password()).isEqualTo("p+ass");
        assertThat(work.payload().connection().rtspUrls().getFirst())
                .isEqualTo("rtsp://10.0.1.5:554/main?token=private-token");
        jobs.complete(work, connections.rtspCatalog(work.payload().connection()));
        JsonNode found = read("/camera-access/jobs/" + id, auth);
        assertThat(found.toString()).doesNotContain("private-token", "p+ass", "rtsp://", "user:");
        assertThat(found.path("candidates").get(0).path("profiles")).hasSize(2);
        assertThat(found.path("device").isNull()).isTrue();
        var command = selected(found, "c0", List.of("p0", "p1"));
        var preview =
                write(post("/camera-access/jobs/" + id + "/import-preview"), auth, command, 200);
        command.put("confirmation", preview.path("confirmation").asText());
        command.put("clientRequestId", requestId());
        var imported = write(post("/camera-access/jobs/" + id + "/import"), auth, command, 200);
        assertThat(imported.path("createdCount").asInt()).isOne();
        assertThat(write(post("/camera-access/jobs/" + id + "/import"), auth, command, 200))
                .isEqualTo(imported);
        var camera =
                read("/cameras/" + imported.path("cameras").get(0).path("cameraId").asText(), auth);
        assertThat(camera.path("lifecycle").asText()).isEqualTo("PENDING_ASSIGNMENT");
        assertThat(camera.path("profiles")).hasSize(2);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                .isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT result_summary FROM camera_access_job WHERE id=?",
                                String.class,
                                Long.valueOf(id)))
                .doesNotContain("private-token", "p+ass", "rtsp://");
        assertThat(
                        new String(
                                jdbc.queryForObject(
                                        "SELECT secret_ciphertext FROM camera_access_job WHERE id=?",
                                        byte[].class,
                                        Long.valueOf(id)),
                                java.nio.charset.StandardCharsets.ISO_8859_1))
                .doesNotContain("private-token");
    }

    @Test
    void discoveredMultichannelUsesStableIdentitiesAndPreservesLocalEditsOnReimport()
            throws Exception {
        var auth = admin();
        var job = discover(auth, null);
        var result = importAll(auth, job);
        String sourceId = result.path("sourceId").asText();
        String cameraId = result.path("cameras").get(0).path("cameraId").asText();
        assertThat(result.path("createdCount").asInt()).isEqualTo(2);
        var asset = read("/cameras/" + cameraId, auth);
        assertThat(asset.path("deviceSummary").toString()).contains("Acme", "Model-X");
        assertThat(
                        asset.path("profiles")
                                .get(0)
                                .path("locatorSummary")
                                .path("editable")
                                .asBoolean())
                .isFalse();
        write(
                put("/cameras/" + cameraId),
                auth,
                Map.of("version", asset.path("version").asText(), "name", "用户保留名称"),
                200);
        var repeated = importAll(auth, discover(auth, sourceId));
        assertThat(repeated.path("existingCount").asInt()).isEqualTo(2);
        assertThat(read("/cameras/" + cameraId, auth).path("name").asText()).isEqualTo("用户保留名称");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(2);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_stream_profile", Integer.class))
                .isEqualTo(4);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_device", Integer.class))
                .isOne();
    }

    @Test
    void cancelFencesLateWorkerAndClearsEncryptedCredentials() throws Exception {
        var auth = admin();
        var first = write(post("/camera-access/jobs"), auth, rtsp(), 202);
        String id = first.path("jobId").asText();
        var work = jobs.claim(Long.parseLong(id));
        write(post("/camera-access/jobs/" + id + "/cancel"), auth, Map.of(), 200);
        assertThatThrownBy(() -> jobs.complete(work, catalog()))
                .isInstanceOf(CameraAdapterException.class);
        assertThat(read("/camera-access/jobs/" + id, auth).path("status").asText())
                .isEqualTo("CANCELLED");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT secret_ciphertext FROM camera_access_job WHERE id=?",
                                byte[].class,
                                Long.valueOf(id)))
                .isNull();
    }

    @Test
    void actorRevocationIsCheckedAgainBeforeCommittingDiscovery() throws Exception {
        var auth = admin();
        var first = write(post("/camera-access/jobs"), auth, rtsp(), 202);
        var work = jobs.claim(first.path("jobId").asLong());
        doThrow(new CameraAdapterException("ACTOR_REVOKED"))
                .when(actorCheck)
                .check(work.userId(), work.payload().sessionId());
        assertThatThrownBy(() -> jobs.complete(work, catalog()))
                .isInstanceOf(CameraAdapterException.class);
        assertThat(read("/camera-access/jobs/" + work.id(), auth).path("status").asText())
                .isEqualTo("RUNNING");
        jobs.fail(work.id(), work.version(), "ACTOR_REVOKED");
        assertThat(read("/camera-access/jobs/" + work.id(), auth).path("status").asText())
                .isEqualTo("CANCELLED");
    }

    @Test
    void expiryPurgesSecretsAndDoesNotAllowImportOrReplay() throws Exception {
        var auth = admin();
        var body = rtsp();
        var job = write(post("/camera-access/jobs"), auth, body, 202);
        jdbc.update(
                "UPDATE camera_access_job SET expires_at=DATEADD('MINUTE',-1,CURRENT_TIMESTAMP) WHERE id=?",
                job.path("jobId").asLong());
        assertThat(
                        read("/camera-access/jobs/" + job.path("jobId").asText(), auth)
                                .path("status")
                                .asText())
                .isEqualTo("EXPIRED");
        write(post("/camera-access/jobs"), auth, body, 409);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT secret_ciphertext FROM camera_access_job", byte[].class))
                .isNull();
    }

    @Test
    void invalidRtspTargetsCredentialsAndDuplicateUrlsHaveNoPersistentSideEffects()
            throws Exception {
        var auth = admin();
        for (String url :
                List.of(
                        "file:///etc/passwd",
                        "rtsp://127.0.0.1/main",
                        "rtsp://169.254.169.254/main",
                        "rtsp://10.0.1.5/main#secret")) {
            var body =
                    Map.of(
                            "clientRequestId",
                            requestId(),
                            "connection",
                            Map.of("method", "RTSP", "name", "test", "rtspUrls", List.of(url)));
            write(post("/camera-access/jobs"), auth, body, 400);
        }
        var contradictory =
                Map.of(
                        "clientRequestId",
                        requestId(),
                        "connection",
                        Map.of(
                                "method",
                                "RTSP",
                                "name",
                                "test",
                                "username",
                                "user",
                                "password",
                                "other",
                                "rtspUrls",
                                List.of("rtsp://user:password@10.0.1.5/main")));
        write(post("/camera-access/jobs"), auth, contradictory, 400);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_access_job", Integer.class))
                .isZero();
    }

    @Test
    void differentSessionCannotReadOrImportAnotherSessionsJob() throws Exception {
        var auth = admin();
        var job = write(post("/camera-access/jobs"), auth, rtsp(), 202);
        var second = login("CameraAdmin", "AdminPass1!");
        mvc.perform(
                        get("/camera-access/jobs/" + job.path("jobId").asText())
                                .session(second.session()))
                .andExpect(
                        org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                                .isNotFound());
    }

    private JsonNode discover(Csrf auth, String sourceId) throws Exception {
        Map<String, Object> connection =
                sourceId == null
                        ? Map.of(
                                "method",
                                "ONVIF",
                                "host",
                                "10.0.1.5",
                                "username",
                                "operator",
                                "password",
                                "device-secret")
                        : Map.of("sourceId", sourceId, "sourceVersion", "0");
        var job =
                write(
                        post("/camera-access/jobs"),
                        auth,
                        Map.of("clientRequestId", requestId(), "connection", connection),
                        202);
        var work = jobs.claim(job.path("jobId").asLong());
        jobs.complete(work, catalog());
        return read("/camera-access/jobs/" + job.path("jobId").asText(), auth);
    }

    private JsonNode importAll(Csrf auth, JsonNode job) throws Exception {
        var command = selected(job, "c0", List.of("p0", "p1"));
        command.put(
                "selections",
                List.of(
                        Map.of(
                                "candidateId",
                                "c0",
                                "profileIds",
                                List.of("p0", "p1"),
                                "defaultProfileId",
                                "p0"),
                        Map.of(
                                "candidateId",
                                "c1",
                                "profileIds",
                                List.of("p0", "p1"),
                                "defaultProfileId",
                                "p0")));
        String route = "/camera-access/jobs/" + job.path("jobId").asText();
        var preview = write(post(route + "/import-preview"), auth, command, 200);
        command.put("confirmation", preview.path("confirmation").asText());
        command.put("clientRequestId", requestId());
        return write(post(route + "/import"), auth, command, 200);
    }

    private Map<String, Object> selected(JsonNode job, String candidate, List<String> profiles) {
        var command = new LinkedHashMap<String, Object>();
        command.put("version", job.path("version").asText());
        command.put(
                "selections",
                List.of(
                        Map.of(
                                "candidateId",
                                candidate,
                                "profileIds",
                                profiles,
                                "defaultProfileId",
                                profiles.getFirst())));
        return command;
    }

    private Map<String, Object> rtsp() {
        var body = new LinkedHashMap<String, Object>();
        body.put("clientRequestId", requestId());
        body.put(
                "connection",
                Map.of(
                        "method",
                        "RTSP",
                        "name",
                        "东门",
                        "rtspUrls",
                        List.of(
                                "rtsp://user:p%2Bass@10.0.1.5/main?token=private-token",
                                "rtsp://10.0.1.5/sub")));
        return body;
    }

    private CameraAccessCatalog catalog() {
        var channels = new ArrayList<CameraAccessCatalog.Channel>();
        for (String channel : List.of("north-sensor", "south-sensor")) {
            var profiles = new ArrayList<CameraAccessCatalog.Profile>();
            for (String profile : List.of("high", "low"))
                profiles.add(
                        new CameraAccessCatalog.Profile(
                                channel + ":" + profile,
                                profile,
                                "UNKNOWN",
                                "H264",
                                1920,
                                1080,
                                25.0,
                                2048,
                                new CameraAccessCatalog.Locator(
                                        "ONVIF",
                                        URI.create("rtsp://10.0.1.5/secret-live-url"),
                                        channel + ":" + profile,
                                        null,
                                        "http://www.onvif.org/ver10/media/wsdl",
                                        URI.create("http://10.0.1.5/onvif/media_service"))));
            channels.add(new CameraAccessCatalog.Channel(channel, channel, profiles, false));
        }
        return new CameraAccessCatalog(
                "ONVIF",
                new CameraAccessCatalog.Device(
                        "serial:unit-x", "设备名称", "Acme", "Model-X", "1.0", "unit-x"),
                channels,
                true,
                List.of());
    }
}
