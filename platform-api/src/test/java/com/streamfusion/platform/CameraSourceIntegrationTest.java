package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;

class CameraSourceIntegrationTest extends CameraTestSupport {
    @Autowired CameraProperties properties;

    @Test
    void credentialsAreEncryptedAndOnlyExplicitKeepOrReplacementCanChangeThem() throws Exception {
        var admin = admin();
        var command = new LinkedHashMap<>(sourceBody(requestId()));
        command.put(
                "credentials",
                List.of(
                        Map.of(
                                "purpose",
                                "RTSP",
                                "username",
                                Map.of("action", "REPLACE", "value", "operator@test"),
                                "password",
                                Map.of("action", "REPLACE", "value", "p@ss%:#"))));
        command.put(
                "endpoints",
                List.of(
                        Map.of(
                                "purpose",
                                "RTSP",
                                "scheme",
                                "rtsp",
                                "host",
                                "10.0.1.5",
                                "port",
                                554,
                                "basePath",
                                "",
                                "authMode",
                                "DRIVER_NEGOTIATED",
                                "credentialPurpose",
                                "RTSP",
                                "tlsPolicy",
                                "SYSTEM_CA")));
        String id = write(post("/camera-sources"), admin, command, 201).path("sourceId").asText();
        String detail = read("/camera-sources/" + id, admin).toString();
        assertThat(detail)
                .contains("已配置")
                .doesNotContain("operator@test", "p@ss", "ciphertext", "nonce");
        byte[] original =
                jdbc.queryForObject(
                        "SELECT secret_ciphertext FROM camera_source_credential WHERE source_id=?",
                        byte[].class,
                        Long.parseLong(id));
        assertThat(new String(original, java.nio.charset.StandardCharsets.UTF_8))
                .doesNotContain("p@ss");
        write(put("/camera-sources/" + id), admin, Map.of("version", "0", "name", "renamed"), 200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT secret_ciphertext FROM camera_source_credential WHERE source_id=?",
                                byte[].class,
                                Long.parseLong(id)))
                .isEqualTo(original);
        write(
                put("/camera-sources/" + id),
                admin,
                Map.of("version", "1", "credentialsRemove", List.of("RTSP")),
                400);
        assertThat(read("/camera-sources/" + id, admin).path("version").asText()).isEqualTo("1");
        write(
                put("/camera-sources/" + id),
                admin,
                Map.of(
                        "version",
                        "1",
                        "credentialsRemove",
                        List.of("RTSP"),
                        "endpointsUpsert",
                        sourceBody(requestId()).get("endpoints")),
                200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_source_credential", Integer.class))
                .isZero();
    }

    @Test
    void sourceReceiptsReplayAcrossActiveKeyRotationAndRejectChangedBodiesOrExpiredKeys()
            throws Exception {
        var admin = admin();
        String key = requestId();
        var command = sourceBody(key);
        String id = write(post("/camera-sources"), admin, command, 201).path("sourceId").asText();
        write(put("/camera-sources/" + id), admin, Map.of("version", "0", "name", "new name"), 200);
        properties.getKeys().put("rotated", "MTExMTExMTExMTExMTExMTExMTExMTExMTExMTExMTE=");
        properties.setActiveKeyId("rotated");
        try {
            assertThat(
                            write(post("/camera-sources"), admin, command, 201)
                                    .path("sourceId")
                                    .asText())
                    .isEqualTo(id);
            var changed = new LinkedHashMap<>(command);
            changed.put("name", "different");
            write(post("/camera-sources"), admin, changed, 409);
            write(
                    post("/camera-sources"),
                    admin,
                    sourceBody((System.currentTimeMillis() - 600_000) + "-" + UUID.randomUUID()),
                    409);
            assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_source", Integer.class))
                    .isEqualTo(1);
        } finally {
            properties.setActiveKeyId("testkey");
            properties.getKeys().remove("rotated");
        }
    }

    @Test
    void invalidEndpointRollsBackSourceCredentialAndReceiptTogether() throws Exception {
        var admin = admin();
        var body = new LinkedHashMap<>(sourceBody(requestId()));
        body.put(
                "credentials",
                List.of(
                        Map.of(
                                "purpose",
                                "RTSP",
                                "username",
                                Map.of("action", "REPLACE", "value", "u"),
                                "password",
                                Map.of("action", "REPLACE", "value", "p"))));
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
                                554,
                                "basePath",
                                "",
                                "authMode",
                                "NONE",
                                "tlsPolicy",
                                "SYSTEM_CA")));
        write(post("/camera-sources"), admin, body, 400);
        for (String table :
                List.of(
                        "camera_source",
                        "camera_source_credential",
                        "camera_source_endpoint",
                        "camera_create_request"))
            assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM " + table, Integer.class))
                    .as(table)
                    .isZero();
    }

    @Test
    void sourceCannotBeDeletedWhileAChannelExistsAndPageRevocationBlocksSuperAdmin()
            throws Exception {
        var admin = admin();
        String id = source(admin).path("sourceId").asText();
        camera(admin, id, "gate");
        mvc.perform(
                        delete("/camera-sources/" + id)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict());
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE module_key='camera'");
        mvc.perform(get("/camera-sources").session(admin.session()))
                .andExpect(status().isForbidden());
    }

    @Test
    void profilesPreserveEncodedSecretsAcrossLabelOnlyEdits() throws Exception {
        var admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        var body = new LinkedHashMap<>(cameraBody(sourceId, "escaped", requestId()));
        body.put(
                "profiles",
                List.of(
                        Map.of(
                                "clientKey",
                                "main",
                                "label",
                                "main",
                                "usageHint",
                                "MAIN",
                                "enabled",
                                true,
                                "locatorKind",
                                "RTSP",
                                "locator",
                                Map.of(
                                        "hostMode",
                                        "SOURCE",
                                        "transport",
                                        "TCP",
                                        "pathSecret",
                                        Map.of("action", "REPLACE", "value", "/a%2Fb"),
                                        "querySecret",
                                        Map.of(
                                                "action",
                                                "REPLACE",
                                                "value",
                                                "secret=a%26b&k=+%2B")))));
        var created = write(post("/cameras"), admin, body, 201);
        String cameraId = created.path("cameraId").asText(),
                profileId = created.path("profiles").get(0).path("streamProfileId").asText();
        byte[] original =
                jdbc.queryForObject(
                        "SELECT rtsp_secret_ciphertext FROM camera_profile_locator WHERE profile_id=?",
                        byte[].class,
                        Long.parseLong(profileId));
        write(
                put("/cameras/" + cameraId + "/profiles/" + profileId),
                admin,
                Map.of("version", "0", "label", "changed"),
                200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT rtsp_secret_ciphertext FROM camera_profile_locator WHERE profile_id=?",
                                byte[].class,
                                Long.parseLong(profileId)))
                .isEqualTo(original);
        assertThat(read("/cameras/" + cameraId, admin).toString())
                .doesNotContain("a%2Fb", "secret=", "ciphertext");
    }

    @Test
    void nullCredentialItemIsValidationFailureAndRetiredPolicyAllowsMetadataMaintenance()
            throws Exception {
        var admin = admin();
        String id = source(admin).path("sourceId").asText();
        write(
                put("/camera-sources/" + id),
                admin,
                Map.of(
                        "version",
                        "0",
                        "credentialsUpsert",
                        java.util.Collections.singletonList(null)),
                400);
        assertThat(read("/camera-sources/" + id, admin).path("version").asText()).isEqualTo("0");
        var policy = properties.getNetworkPolicies().remove("test");
        try {
            write(
                    put("/camera-sources/" + id),
                    admin,
                    Map.of("version", "0", "name", "retired policy source"),
                    200);
            write(
                    put("/camera-sources/" + id),
                    admin,
                    Map.of(
                            "version",
                            "1",
                            "endpointsUpsert",
                            sourceBody(requestId()).get("endpoints")),
                    400);
        } finally {
            properties.getNetworkPolicies().put("test", policy);
        }
    }
}
