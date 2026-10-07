package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.never;
import static org.mockito.Mockito.verify;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.streamfusion.platform.camera.access.adapter.CameraHttpTransport;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.pojo.dto.CameraCreateDto;
import com.streamfusion.platform.camera.pojo.dto.CameraSourceWriteDto;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;

/** Manual records have no discovery/media prerequisite, while old saved identities remain valid. */
class CameraManualArchiveIntegrationTest extends CameraTestSupport {
    @Autowired CameraProperties properties;
    @Autowired CameraCryptoService crypto;
    @MockitoSpyBean CameraHttpTransport transport;

    @Test
    void savesUnknownDeviceWithoutNetworkPolicyProfilesOrExternalRequests() throws Exception {
        var admin = admin();
        var policies = new LinkedHashMap<>(properties.getNetworkPolicies());
        properties.getNetworkPolicies().clear();
        try {
            assertThat(read("/cameras/options", admin).path("manualStorageReady").asBoolean())
                    .isTrue();
            JsonNode created = write(post("/cameras"), admin, archive(requestId()), 201);
            String cameraId = created.path("cameraId").asText();
            String sourceId = created.path("sourceId").asText();
            assertThat(created.path("profiles")).isEmpty();
            assertThat(created.path("defaultPreviewProfileId").isNull()).isTrue();
            assertThat(created.path("lifecycle").asText()).isEqualTo("PENDING_ASSIGNMENT");
            var detail = read("/cameras/" + cameraId, admin);
            assertThat(detail.path("connectionCategory").asText()).isEqualTo("DEVICE");
            assertThat(detail.has("sourceType")).isFalse();
            assertThat(detail.path("vendorHint").asText()).isEqualTo("现场待确认厂商");
            assertThat(detail.has("deviceSummary")).isFalse();
            assertThat(detail.path("profiles")).isEmpty();
            var source = read("/camera-sources/" + sourceId, admin);
            assertThat(source.path("adapterType").isNull()).isTrue();
            assertThat(source.path("networkPolicyKey").isNull()).isTrue();
            assertThat(source.path("rtspPort").isNull()).isTrue();
            assertThat(source.path("endpointEditable").asBoolean()).isTrue();
            assertThat(source.path("endpoints").get(0).path("purpose").asText())
                    .isEqualTo("DEVICE_HTTP");
            assertThat(source.path("endpoints").get(0).path("port").asInt()).isEqualTo(80);
            assertThat(source.toString()).doesNotContain("offline-user", "offline-password");
            for (String table :
                    List.of(
                            "camera_device",
                            "camera_stream_profile",
                            "camera_profile_locator",
                            "camera_access_job"))
                assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM " + table, Integer.class))
                        .isZero();
            byte[] ciphertext = credentialBytes(sourceId);
            assertThat(new String(ciphertext, java.nio.charset.StandardCharsets.UTF_8))
                    .doesNotContain("offline-password");
            verify(transport, never()).open(any());
            write(
                    put("/camera-sources/" + sourceId),
                    admin,
                    Map.of("version", "0", "enabled", false),
                    200);
            write(
                    put("/camera-sources/" + sourceId),
                    admin,
                    Map.of("version", "1", "enabled", true),
                    200);
            assertThat(credentialBytes(sourceId)).isEqualTo(ciphertext);
        } finally {
            properties.setNetworkPolicies(policies);
        }
    }

    @Test
    void concurrentRetriesCreateOneInternalSourceAndChangingTheRequestConflicts() throws Exception {
        var admin = admin();
        var body = archive(requestId());
        var latch = new CountDownLatch(1);
        JsonNode created;
        try (var executor = Executors.newFixedThreadPool(2)) {
            var first =
                    executor.submit(
                            () -> {
                                latch.await();
                                return write(post("/cameras"), admin, body, 201);
                            });
            var second =
                    executor.submit(
                            () -> {
                                latch.await();
                                return write(post("/cameras"), admin, body, 201);
                            });
            latch.countDown();
            created = first.get(15, TimeUnit.SECONDS);
            assertThat(second.get(15, TimeUnit.SECONDS)).isEqualTo(created);
        }
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                .isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_create_request WHERE operation='CREATE_CAMERA' AND parent_resource_id IS NULL",
                                Integer.class))
                .isOne();
        var changed = new LinkedHashMap<>(body);
        changed.put("name", "另一个名称");
        write(post("/cameras"), admin, changed, 409);
        write(
                put("/cameras/" + created.path("cameraId").asText()),
                admin,
                Map.of("version", "0", "name", "改名后的档案"),
                200);
        assertThat(write(post("/cameras"), admin, body, 201)).isEqualTo(created);
    }

    @Test
    void rejectsAmbiguousOrInvalidArchivesWithoutPartialRows() throws Exception {
        var admin = admin();
        var mixed = new LinkedHashMap<>(archive(requestId()));
        mixed.put("sourceId", "10001");
        mixed.put("sourceVersion", "0");
        write(post("/cameras"), admin, mixed, 400);
        for (Map<String, Object> connection :
                List.of(
                        Map.<String, Object>of("host", "192.0.2.4", "adapterType", "HIK_PLATFORM"),
                        Map.<String, Object>of(
                                "host", "192.0.2.4", "adapterType", "UNKNOWN_DRIVER"),
                        Map.<String, Object>of("host", "rtsp://192.0.2.4/path"),
                        Map.<String, Object>of("host", "192.0.2.4", "username", "unpaired"))) {
            var invalid = new LinkedHashMap<>(archive(requestId()));
            invalid.put("connection", connection);
            write(post("/cameras"), admin, invalid, 400);
        }
        var invalid = new LinkedHashMap<>(archive(requestId()));
        invalid.put("defaultProfileClientKey", "unobserved");
        write(post("/cameras"), admin, invalid, 400);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_create_request", Integer.class))
                .isZero();
    }

    @Test
    void editsUnboundConnectionButPreservesCiphertextAndProtectsBoundIdentity() throws Exception {
        var admin = admin();
        var created = write(post("/cameras"), admin, archive(requestId()), 201);
        String sourceId = created.path("sourceId").asText();
        byte[] ciphertext = credentialBytes(sourceId);
        var endpoint =
                Map.<String, Object>of(
                        "purpose",
                        "DEVICE_HTTP",
                        "scheme",
                        "http",
                        "host",
                        "192.0.2.9",
                        "port",
                        8080,
                        "basePath",
                        "",
                        "authMode",
                        "DRIVER_NEGOTIATED",
                        "credentialPurpose",
                        "DEVICE_HTTP",
                        "tlsPolicy",
                        "SYSTEM_CA");
        write(
                put("/camera-sources/" + sourceId),
                admin,
                Map.of(
                        "version",
                        "0",
                        "adapterType",
                        "ONVIF",
                        "vendorHint",
                        "人工提示",
                        "endpointsUpsert",
                        List.of(endpoint)),
                200);
        assertThat(credentialBytes(sourceId)).isEqualTo(ciphertext);
        var clear = new LinkedHashMap<String, Object>();
        clear.put("version", "1");
        clear.put("adapterType", null);
        clear.put("vendorHint", null);
        write(put("/camera-sources/" + sourceId), admin, clear, 200);
        assertThat(read("/camera-sources/" + sourceId, admin).path("adapterType").isNull())
                .isTrue();
        jdbc.update(
                "UPDATE camera_channel SET mapping_origin='ADAPTER' WHERE id=?",
                Long.valueOf(created.path("cameraId").asText()));
        assertThat(read("/camera-sources/" + sourceId, admin).path("endpointEditable").asBoolean())
                .isFalse();
        write(
                put("/camera-sources/" + sourceId),
                admin,
                Map.of("version", "2", "adapterType", "DAHUA"),
                409);
        var movedEndpoint = new LinkedHashMap<>(endpoint);
        movedEndpoint.put("host", "192.0.2.10");
        write(
                put("/camera-sources/" + sourceId),
                admin,
                Map.of("version", "2", "endpointsUpsert", List.of(movedEndpoint)),
                409);
        assertThat(credentialBytes(sourceId)).isEqualTo(ciphertext);
    }

    @Test
    void keepsTheFullCameraNameWhileBoundingTheInternalConnectionName() throws Exception {
        var admin = admin();
        var body = new LinkedHashMap<>(archive(requestId()));
        String name = "机".repeat(128);
        body.put("name", name);
        var created = write(post("/cameras"), admin, body, 201);
        assertThat(
                        read("/cameras/" + created.path("cameraId").asText(), admin)
                                .path("name")
                                .asText())
                .isEqualTo(name);
        assertThat(
                        read("/camera-sources/" + created.path("sourceId").asText(), admin)
                                .path("name")
                                .asText())
                .isEqualTo("机".repeat(100));
    }

    @Test
    void replaysPreExtensionSourceAndCameraFingerprints() throws Exception {
        var admin = admin();
        var sourceBody = sourceBody(requestId());
        var source = write(post("/camera-sources"), admin, sourceBody, 201);
        String sourceId = source.path("sourceId").asText();
        var sourceDto = json.convertValue(sourceBody, CameraSourceWriteDto.class);
        ObjectNode oldSourceShape = json.valueToTree(sourceDto);
        oldSourceShape.remove(List.of("connectionCategory", "vendorHint"));
        replaceReceiptFingerprint("CREATE_SOURCE", null, oldSourceShape, sourceId);
        assertThat(write(post("/camera-sources"), admin, sourceBody, 201)).isEqualTo(source);
        var cameraBody = cameraBody(sourceId, "旧接口", requestId());
        var camera = write(post("/cameras"), admin, cameraBody, 201);
        var cameraDto = json.convertValue(cameraBody, CameraCreateDto.class);
        ObjectNode oldCameraShape = json.valueToTree(cameraDto);
        oldCameraShape.remove("connection");
        replaceReceiptFingerprint(
                "CREATE_CAMERA",
                Long.valueOf(sourceId),
                oldCameraShape,
                camera.path("cameraId").asText());
        assertThat(write(post("/cameras"), admin, cameraBody, 201)).isEqualTo(camera);
    }

    private void replaceReceiptFingerprint(
            String operation, Long parentId, ObjectNode command, String resourceId) {
        var payload = new LinkedHashMap<String, Object>();
        payload.put("operation", operation);
        payload.put("parentId", parentId);
        payload.put("command", command);
        jdbc.update(
                "UPDATE camera_create_request SET request_fingerprint=? WHERE operation=? AND resource_id=?",
                crypto.digest("testkey", "create", payload),
                operation,
                Long.valueOf(resourceId));
    }

    private byte[] credentialBytes(String sourceId) {
        return jdbc.queryForObject(
                "SELECT secret_ciphertext FROM camera_source_credential WHERE source_id=?",
                byte[].class,
                Long.valueOf(sourceId));
    }

    private Map<String, Object> archive(String requestId) {
        return Map.of(
                "clientRequestId",
                requestId,
                "name",
                "离线设备档案",
                "connection",
                Map.of(
                        "host",
                        "192.0.2.4",
                        "vendorHint",
                        "现场待确认厂商",
                        "username",
                        "offline-user",
                        "password",
                        "offline-password"));
    }
}
