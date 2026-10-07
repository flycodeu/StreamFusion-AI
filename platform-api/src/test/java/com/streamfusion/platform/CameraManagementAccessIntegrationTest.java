package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.pojo.*;
import com.streamfusion.platform.camera.access.service.*;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/**
 * HTTP + transactional SQL fixtures. Catalogues are deterministic observations, not real devices.
 */
class CameraManagementAccessIntegrationTest extends CameraTestSupport {
    @MockitoBean CameraAccessWorker worker;
    @MockitoBean CameraJobActor actorCheck;
    @Autowired CameraAccessJobsService jobs;
    @Autowired CameraConnectionRules connections;
    @Autowired CameraCryptoService crypto;

    @Test
    void zeroProfilePlatformPagesReuseExplicitSourceAndStableExternalKeys() throws Exception {
        var auth = admin();
        var first =
                directory(
                        auth,
                        Map.of(
                                "method",
                                "HIK_PLATFORM",
                                "host",
                                "10.0.1.5",
                                "username",
                                "app-key",
                                "password",
                                "app-secret"),
                        platform("index-A", 1, true));
        assertThat(first.path("page").path("hasMore").asBoolean()).isTrue();
        var created = importOne(auth, first, Map.of());
        String camera = created.path("cameras").get(0).path("cameraId").asText();
        assertThat(read("/cameras/" + camera, auth).path("profiles")).isEmpty();
        var connection =
                Map.<String, Object>of(
                        "sourceId",
                        created.path("sourceId").asText(),
                        "sourceVersion",
                        created.path("sourceVersion").asText(),
                        "pageNumber",
                        2,
                        "pageSize",
                        100);
        var next = directory(auth, connection, platform("index-B", 2, false));
        var second = importOne(auth, next, Map.of());
        assertThat(second.path("sourceId")).isEqualTo(created.path("sourceId"));
        var repeated =
                importOne(
                        auth, directory(auth, connection, platform("index-B", 2, false)), Map.of());
        assertThat(repeated.path("createdCount").asInt()).isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                .isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(2);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_stream_profile", Integer.class))
                .isZero();
    }

    @Test
    void explicitBindingKeepsManualCameraIdentityAndLocalFieldsWithoutIpGuessing()
            throws Exception {
        var auth = admin();
        var manual =
                write(
                        post("/cameras"),
                        auth,
                        Map.of(
                                "clientRequestId",
                                requestId(),
                                "name",
                                "本地档案名称",
                                "connection",
                                Map.of(
                                        "host",
                                        "10.0.1.5",
                                        "username",
                                        "operator",
                                        "password",
                                        "secret")),
                        201);
        String cameraId = manual.path("cameraId").asText();
        if (cameraId.isBlank()) cameraId = manual.path("id").asText();
        var detail = read("/cameras/" + cameraId, auth);
        String sourceId = detail.path("sourceId").asText();
        var source = read("/camera-sources/" + sourceId, auth);
        var discovered =
                directory(
                        auth,
                        Map.of(
                                "sourceId",
                                sourceId,
                                "sourceVersion",
                                source.path("version").asText(),
                                "method",
                                "ONVIF"),
                        new CameraAccessCatalog(
                                "ONVIF",
                                new CameraAccessCatalog.Device(
                                        "serial:fixture", "设备名称", "Vendor", null, null, "fixture"),
                                List.of(
                                        new CameraAccessCatalog.Channel(
                                                "opaque-sensor",
                                                "设备通道名称",
                                                List.of(
                                                        new CameraAccessCatalog.Profile(
                                                                "opaque-profile",
                                                                "设备档案",
                                                                "UNKNOWN",
                                                                null,
                                                                null,
                                                                null,
                                                                null,
                                                                null,
                                                                new CameraAccessCatalog.Locator(
                                                                        "ONVIF",
                                                                        null,
                                                                        "opaque-profile",
                                                                        null,
                                                                        "http://www.onvif.org/ver10/media/wsdl",
                                                                        java.net.URI.create(
                                                                                "http://10.0.1.5/onvif/media_service")))),
                                                false)),
                                true,
                                List.of()));
        var result =
                importOne(
                        auth,
                        discovered,
                        Map.of(
                                "targetCameraId",
                                cameraId,
                                "targetCameraVersion",
                                detail.path("version").asText(),
                                "selections",
                                List.of(
                                        Map.of(
                                                "candidateId",
                                                "c0",
                                                "profileIds",
                                                List.of("p0"),
                                                "defaultProfileId",
                                                "p0"))));
        assertThat(result.path("createdCount").asInt()).isZero();
        assertThat(result.path("cameras").get(0).path("cameraId").asText()).isEqualTo(cameraId);
        var after = read("/cameras/" + cameraId, auth);
        assertThat(after.path("name").asText()).isEqualTo("本地档案名称");
        assertThat(after.path("lifecycle")).isEqualTo(detail.path("lifecycle"));
        assertThat(after.path("profiles")).hasSize(1);
        assertThat(after.path("defaultPreviewProfileId").asText())
                .isEqualTo(after.path("profiles").get(0).path("streamProfileId").asText());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT external_channel_key FROM camera_channel WHERE id=?",
                                String.class,
                                Long.valueOf(cameraId)))
                .isEqualTo("opaque-sensor");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT purpose FROM camera_source_credential WHERE source_id=?",
                                String.class,
                                Long.valueOf(sourceId)))
                .isEqualTo("DEVICE_HTTP");
    }

    @Test
    void scanJobSharesFencesHasNoImportAndNeverCreatesSources() throws Exception {
        var auth = admin();
        var input =
                Map.of(
                        "clientRequestId",
                        requestId(),
                        "networkPolicyKey",
                        "test",
                        "cidr",
                        "10.0.1.5/32",
                        "ports",
                        List.of(80, 443));
        var ticket = write(post("/camera-access/scan-jobs"), auth, input, 202);
        assertThat(write(post("/camera-access/scan-jobs"), auth, input, 202).path("jobId"))
                .isEqualTo(ticket.path("jobId"));
        var work = jobs.claim(ticket.path("jobId").asLong());
        assertThat(work.payload().connection()).isNull();
        jobs.completeScan(
                work,
                new CameraNetworkScan.Result(
                        List.of(
                                new CameraNetworkScan.Host(
                                        "h0", "10.0.1.5", List.of(80), "PORT_OPEN_ONLY")),
                        1,
                        1,
                        true));
        var result = read("/camera-access/jobs/" + ticket.path("jobId").asText(), auth);
        assertThat(result.path("kind").asText()).isEqualTo("SCAN");
        assertThat(result.path("hosts").get(0).path("identityConfidence").asText())
                .isEqualTo("PORT_OPEN_ONLY");
        write(
                post("/camera-access/jobs/" + ticket.path("jobId").asText() + "/import-preview"),
                auth,
                selection(result),
                400);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                .isZero();
        var queued =
                write(
                        post("/camera-access/scan-jobs"),
                        auth,
                        Map.of(
                                "clientRequestId",
                                requestId(),
                                "networkPolicyKey",
                                "test",
                                "cidr",
                                "10.0.1.5/32",
                                "ports",
                                List.of(80)),
                        202);
        var active = jobs.claim(queued.path("jobId").asLong());
        write(
                post("/camera-access/jobs/" + queued.path("jobId").asText() + "/cancel"),
                auth,
                Map.of(),
                200);
        assertThatThrownBy(() -> jobs.checkWork(active)).hasMessage("JOB_NO_LONGER_ACTIVE");
    }

    @Test
    void repeatedRtspJobReusesSameSourceConfigurationWithoutDuplicatingAsset() throws Exception {
        var auth = admin();
        JsonNode imported = null;
        String cameraId = null;
        for (int iteration = 0; iteration < 2; iteration++) {
            var connection = new LinkedHashMap<String, Object>();
            connection.put("method", "RTSP");
            connection.put("name", "RTSP档案");
            connection.put("rtspUrls", List.of("rtsp://10.0.1.5/main?opaque=example"));
            if (imported != null) {
                connection.put("sourceId", imported.path("sourceId").asText());
                connection.put("sourceVersion", imported.path("sourceVersion").asText());
            }
            var job =
                    write(
                            post("/camera-access/jobs"),
                            auth,
                            Map.of("clientRequestId", requestId(), "connection", connection),
                            202);
            var work = jobs.claim(job.path("jobId").asLong());
            jobs.complete(work, connections.rtspCatalog(work.payload().connection()));
            var complete = read("/camera-access/jobs/" + job.path("jobId").asText(), auth);
            imported =
                    importOne(
                            auth,
                            complete,
                            Map.of(
                                    "selections",
                                    List.of(
                                            Map.of(
                                                    "candidateId",
                                                    "c0",
                                                    "profileIds",
                                                    List.of("p0")))));
            if (cameraId == null)
                cameraId = imported.path("cameras").get(0).path("cameraId").asText();
            else
                assertThat(imported.path("cameras").get(0).path("cameraId").asText())
                        .isEqualTo(cameraId);
        }
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_stream_profile", Integer.class))
                .isOne();
    }

    @Test
    void rtspConfigurationIdentityIsStableAcrossJobsAndLegacyNullExtensionsDoNotChangeDigest()
            throws Exception {
        var input =
                new CameraConnection(
                        "RTSP",
                        "旧名称",
                        "10.0.1.5",
                        554,
                        "rtsp",
                        null,
                        null,
                        554,
                        "test",
                        null,
                        null,
                        List.of("rtsp://10.0.1.5/main"));
        var first = connections.rtspCatalog(input);
        var next = connections.rtspCatalog(input);
        assertThat(first.channels().getFirst().externalKey())
                .isEqualTo(next.channels().getFirst().externalKey());
        assertThat(first.channels().getFirst().profiles().getFirst().externalKey())
                .isEqualTo(next.channels().getFirst().profiles().getFirst().externalKey());
        var legacy = json.valueToTree(input);
        assertThat(legacy.has("pageNumber")).isFalse();
        assertThat(legacy.has("pageSize")).isFalse();
        assertThat(crypto.canonical(input)).isEqualTo(crypto.canonical(legacy));
        assertThat(
                        crypto.canonical(
                                new ImportSelection(
                                        List.of(
                                                new ImportSelection.Selection(
                                                        "c0", List.of(), null)),
                                        null,
                                        null)))
                .doesNotContain("targetCamera");
    }

    private CameraAccessCatalog platform(String key, int page, boolean more) {
        return new CameraAccessCatalog(
                "HIK_PLATFORM",
                null,
                List.of(new CameraAccessCatalog.Channel(key, key, List.of(), false)),
                true,
                List.of(),
                new CameraAccessCatalog.Page(page, 100, 101L, more));
    }

    private JsonNode directory(
            Csrf auth, Map<String, Object> connection, CameraAccessCatalog catalog)
            throws Exception {
        var ticket =
                write(
                        post("/camera-access/jobs"),
                        auth,
                        Map.of("clientRequestId", requestId(), "connection", connection),
                        202);
        var work = jobs.claim(ticket.path("jobId").asLong());
        jobs.complete(work, catalog);
        return read("/camera-access/jobs/" + ticket.path("jobId").asText(), auth);
    }

    private LinkedHashMap<String, Object> selection(JsonNode job) {
        var command = new LinkedHashMap<String, Object>();
        command.put("version", job.path("version").asText());
        command.put("selections", List.of(Map.of("candidateId", "c0", "profileIds", List.of())));
        return command;
    }

    private JsonNode importOne(Csrf auth, JsonNode job, Map<String, Object> extra)
            throws Exception {
        var command = selection(job);
        command.putAll(extra);
        String route = "/camera-access/jobs/" + job.path("jobId").asText();
        var preview = write(post(route + "/import-preview"), auth, command, 200);
        command.put("confirmation", preview.path("confirmation").asText());
        command.put("clientRequestId", requestId());
        return write(post(route + "/import"), auth, command, 200);
    }
}
