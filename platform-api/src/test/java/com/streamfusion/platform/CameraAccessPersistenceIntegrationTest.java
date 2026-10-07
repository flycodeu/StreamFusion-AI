package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessCatalog;
import com.streamfusion.platform.camera.access.service.CameraAccessJobsService;
import com.streamfusion.platform.camera.access.service.CameraAccessWorker;
import com.streamfusion.platform.camera.access.service.CameraJobActor;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.support.CameraTestSupport;
import java.net.URI;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/** Discovery is fixture-backed; all import, source, group and locator writes use real SQL. */
class CameraAccessPersistenceIntegrationTest extends CameraTestSupport {
    @MockitoBean CameraAccessWorker worker;
    @MockitoBean CameraJobActor actorCheck;
    @Autowired CameraAccessJobsService jobs;
    @Autowired CameraCryptoService crypto;

    @Test
    void importConfirmationTracksGroupChangesAndPreservesLocalDefaultsOnRepeat() throws Exception {
        var auth = admin();
        var group = write(post("/camera-groups"), auth, Map.of("name", "北区"), 201);
        String groupId = group.path("groupId").asText();
        var job = discover(auth, null, catalog(false, false));
        var command = selection(job, groupId, List.of("p0"));
        confirm(auth, job, command);
        write(put("/camera-groups/" + groupId), auth, Map.of("version", "0", "name", "新北区"), 200);
        write(post(route(job) + "/import"), auth, command, 409);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                .isZero();
        confirm(auth, job, command);
        var imported = write(post(route(job) + "/import"), auth, command, 200);
        String sourceId = imported.path("sourceId").asText();
        String cameraId = imported.path("cameras").get(0).path("cameraId").asText();
        var asset = read("/cameras/" + cameraId, auth);
        assertThat(asset.path("lifecycle").asText()).isEqualTo("ENABLED");
        assertThat(asset.path("groupId").asText()).isEqualTo(groupId);
        String profileId = asset.path("profiles").get(0).path("streamProfileId").asText();
        var local = new LinkedHashMap<String, Object>();
        local.put("version", "0");
        local.put("name", "本地名称");
        local.put("defaultPreviewProfileId", null);
        write(put("/cameras/" + cameraId), auth, local, 200);
        write(
                put("/cameras/" + cameraId + "/profiles/" + profileId),
                auth,
                Map.of("version", "0", "label", "本地主流", "usageHint", "MAIN"),
                200);
        var second = discover(auth, sourceId, catalog(true, false));
        var repeated = selection(second, null, List.of("p0", "p1"));
        confirm(auth, second, repeated);
        var replay = write(post(route(second) + "/import"), auth, repeated, 200);
        assertThat(replay.path("existingCount").asInt()).isOne();
        var current = read("/cameras/" + cameraId, auth);
        assertThat(current.path("name").asText()).isEqualTo("本地名称");
        assertThat(current.path("groupId").asText()).isEqualTo(groupId);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT default_preview_profile_id FROM camera_channel WHERE id=?",
                                Long.class,
                                Long.valueOf(cameraId)))
                .isNull();
        assertThat(current.hasNonNull("defaultPreviewProfileId")).isFalse();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT label FROM camera_stream_profile WHERE id=?",
                                String.class,
                                Long.valueOf(profileId)))
                .isEqualTo("本地主流");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT usage_origin FROM camera_stream_profile WHERE id=?",
                                String.class,
                                Long.valueOf(profileId)))
                .isEqualTo("MANUAL");
        assertThat(current.path("profiles")).hasSize(2);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
    }

    @Test
    void invalidSecondNativeLocatorRollsBackEntireSelectedImport() throws Exception {
        var auth = admin();
        var job = discover(auth, null, catalog(true, true));
        var command = selection(job, null, List.of("p0", "p1"));
        confirm(auth, job, command);
        write(post(route(job) + "/import"), auth, command, 400);
        for (String table :
                List.of(
                        "camera_source",
                        "camera_device",
                        "camera_channel",
                        "camera_stream_profile",
                        "camera_profile_locator"))
            assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM " + table, Integer.class))
                    .isZero();
        assertThat(read(route(job), auth).path("status").asText()).isEqualTo("SUCCEEDED");
    }

    @Test
    void nativeIdentityIsEncryptedAndCannotBeReplacedThroughManualEditor() throws Exception {
        var auth = admin();
        var job = discover(auth, null, catalog(false, false));
        var command = selection(job, null, List.of("p0"));
        confirm(auth, job, command);
        var imported = write(post(route(job) + "/import"), auth, command, 200);
        String cameraId = imported.path("cameras").get(0).path("cameraId").asText();
        var asset = read("/cameras/" + cameraId, auth);
        String profileId = asset.path("profiles").get(0).path("streamProfileId").asText();
        assertThat(asset.toString())
                .doesNotContain("secret-token", "private-playback", "media_service");
        var row =
                jdbc.queryForMap(
                        "SELECT * FROM camera_profile_locator WHERE profile_id=?",
                        Long.valueOf(profileId));
        var plain =
                crypto.decrypt(
                        "camera_profile_locator/"
                                + imported.path("sourceId").asText()
                                + "/"
                                + profileId
                                + "/ONVIF",
                        new CameraCryptoService.Envelope(
                                (String) row.get("protocol_encryption_key_id"),
                                (byte[]) row.get("protocol_secret_nonce"),
                                (byte[]) row.get("protocol_secret_ciphertext"),
                                (byte[]) row.get("protocol_secret_tag")));
        assertThat(plain.path("profileToken").asText()).isEqualTo("secret-token-main");
        assertThat(plain.path("serviceNamespace").asText())
                .isEqualTo("http://www.onvif.org/ver20/media/wsdl");
        assertThat(plain.toString()).doesNotContain("private-playback");
        String sourceId = imported.path("sourceId").asText();
        var source = read("/camera-sources/" + sourceId, auth);
        assertThat(source.path("endpointEditable").asBoolean()).isFalse();
        write(
                put("/camera-sources/" + sourceId),
                auth,
                Map.of(
                        "version",
                        "0",
                        "endpointsUpsert",
                        List.of(
                                Map.of(
                                        "purpose",
                                        "ONVIF",
                                        "scheme",
                                        "http",
                                        "host",
                                        "10.0.1.9",
                                        "port",
                                        80,
                                        "basePath",
                                        "/onvif/device_service",
                                        "authMode",
                                        "NONE",
                                        "tlsPolicy",
                                        "SYSTEM_CA"))),
                409);
        write(
                put("/camera-sources/" + sourceId),
                auth,
                Map.of(
                        "version",
                        "0",
                        "rtspPort",
                        8554,
                        "credentialsUpsert",
                        List.of(
                                Map.of(
                                        "purpose",
                                        "ONVIF",
                                        "username",
                                        Map.of("action", "REPLACE", "value", "new-user"),
                                        "password",
                                        Map.of("action", "REPLACE", "value", "new-secret"))),
                        "endpointsUpsert",
                        List.of(
                                Map.of(
                                        "purpose",
                                        "ONVIF",
                                        "scheme",
                                        "http",
                                        "host",
                                        "10.0.1.5",
                                        "port",
                                        80,
                                        "basePath",
                                        "/onvif/device_service",
                                        "authMode",
                                        "DRIVER_NEGOTIATED",
                                        "credentialPurpose",
                                        "ONVIF",
                                        "tlsPolicy",
                                        "SYSTEM_CA"))),
                200);
        assertThat(read("/camera-sources/" + sourceId, auth).path("rtspPort").asInt())
                .isEqualTo(8554);
        write(
                put("/cameras/" + cameraId + "/profiles/" + profileId),
                auth,
                Map.of("version", "0", "locatorKind", "RTSP"),
                409);
        write(
                put("/cameras/" + cameraId + "/profiles/" + profileId),
                auth,
                Map.of(
                        "version",
                        "0",
                        "locator",
                        Map.of("fullUrl", "rtsp://10.0.1.5/new", "transport", "TCP")),
                409);
    }

    @Test
    void completeRtspUrlEditorPreservesEscapesAndRejectsEmbeddedCredentials() throws Exception {
        var auth = admin();
        var source = source(auth);
        var asset = camera(auth, source.path("sourceId").asText(), "手工");
        String cid = asset.path("cameraId").asText();
        String pid = asset.path("profiles").get(0).path("streamProfileId").asText();
        write(
                put("/cameras/" + cid + "/profiles/" + pid),
                auth,
                Map.of(
                        "version",
                        "0",
                        "locator",
                        Map.of(
                                "fullUrl",
                                "rtsp://10.0.2.5:8554/a%2Fb?sig=A%2Bb",
                                "transport",
                                "TCP")),
                200);
        var row =
                jdbc.queryForMap(
                        "SELECT * FROM camera_profile_locator WHERE profile_id=?",
                        Long.valueOf(pid));
        var plain =
                crypto.decrypt(
                        "camera_profile_locator/"
                                + source.path("sourceId").asText()
                                + "/"
                                + pid
                                + "/RTSP",
                        new CameraCryptoService.Envelope(
                                (String) row.get("rtsp_encryption_key_id"),
                                (byte[]) row.get("rtsp_secret_nonce"),
                                (byte[]) row.get("rtsp_secret_ciphertext"),
                                (byte[]) row.get("rtsp_secret_tag")));
        assertThat(plain.path("encodedPath").asText()).isEqualTo("/a%2Fb");
        assertThat(plain.path("rawQuery").asText()).isEqualTo("sig=A%2Bb");
        for (String url :
                List.of("rtsp://user:pass@10.0.1.5/main", "rtsp://127.0.0.1/main", "file:///tmp/a"))
            write(
                    put("/cameras/" + cid + "/profiles/" + pid),
                    auth,
                    Map.of("version", "1", "locator", Map.of("fullUrl", url, "transport", "TCP")),
                    400);
        assertThat(read("/cameras/" + cid, auth).toString()).doesNotContain("sig=", "/a%2F");
    }

    private JsonNode discover(Csrf auth, String sourceId, CameraAccessCatalog catalog)
            throws Exception {
        var connection =
                sourceId == null
                        ? Map.of("method", "ONVIF", "host", "10.0.1.5")
                        : Map.of("sourceId", sourceId, "sourceVersion", "0");
        var created =
                write(
                        post("/camera-access/jobs"),
                        auth,
                        Map.of("clientRequestId", requestId(), "connection", connection),
                        202);
        var work = jobs.claim(created.path("jobId").asLong());
        jobs.complete(work, catalog);
        return read(route(created), auth);
    }

    private Map<String, Object> selection(JsonNode job, String group, List<String> profiles) {
        var command = new LinkedHashMap<String, Object>();
        command.put("version", job.path("version").asText());
        command.put("groupId", group);
        command.put(
                "selections",
                List.of(
                        Map.of(
                                "candidateId",
                                "c0",
                                "profileIds",
                                profiles,
                                "defaultProfileId",
                                "p0")));
        return command;
    }

    private void confirm(Csrf auth, JsonNode job, Map<String, Object> command) throws Exception {
        var preview = write(post(route(job) + "/import-preview"), auth, command, 200);
        command.put("confirmation", preview.path("confirmation").asText());
        command.put("clientRequestId", requestId());
    }

    private static String route(JsonNode job) {
        return "/camera-access/jobs/" + job.path("jobId").asText();
    }

    private CameraAccessCatalog catalog(boolean extra, boolean invalid) {
        var profiles = new ArrayList<CameraAccessCatalog.Profile>();
        profiles.add(profile("main", "secret-token-main"));
        if (extra) profiles.add(profile("extra", invalid ? "" : "secret-token-extra"));
        return new CameraAccessCatalog(
                "ONVIF",
                new CameraAccessCatalog.Device("serial:unit", "Device", "Acme", "X", "1", "unit"),
                List.of(new CameraAccessCatalog.Channel("sensor-main", "设备名称", profiles, false)),
                true,
                List.of());
    }

    private CameraAccessCatalog.Profile profile(String key, String token) {
        return new CameraAccessCatalog.Profile(
                key,
                key,
                "UNKNOWN",
                "H264",
                1920,
                1080,
                25.0,
                2048,
                new CameraAccessCatalog.Locator(
                        "ONVIF",
                        URI.create("rtsp://10.0.1.5/private-playback"),
                        token,
                        null,
                        "http://www.onvif.org/ver20/media/wsdl",
                        URI.create("http://10.0.1.5/onvif/media_service")));
    }
}
