package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.delete;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.dao.DataIntegrityViolationException;

class CameraAssetIntegrationTest extends CameraTestSupport {
    @Test
    void createsIndependentChannelsAndProfilesAndReplaysAfterAssetEdits() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        var command = cameraBody(sourceId, "东门", requestId());
        JsonNode first = write(post("/cameras"), admin, command, 201);
        String id = first.path("cameraId").asText();
        assertThat(first.path("lifecycle").asText()).isEqualTo("PENDING_ASSIGNMENT");
        assertThat(first.path("profiles")).hasSize(2);
        assertThat(first.path("profiles").get(0).path("streamProfileId").asText())
                .isNotEqualTo(first.path("profiles").get(1).path("streamProfileId").asText());
        write(put("/cameras/" + id), admin, Map.of("version", "0", "name", "东门新名称"), 200);
        JsonNode replay = write(post("/cameras"), admin, command, 201);
        assertThat(replay).isEqualTo(first);
        assertThat(read("/cameras/" + id, admin).path("name").asText()).isEqualTo("东门新名称");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_stream_profile", Integer.class))
                .isEqualTo(2);
        JsonNode second = camera(admin, sourceId, "西门");
        assertThat(second.path("cameraId").asText()).isNotEqualTo(id);
        var different = new LinkedHashMap<>(command);
        different.put("name", "同键不同内容");
        write(post("/cameras"), admin, different, 409);
        String detail = read("/cameras/" + id, admin).toString();
        assertThat(detail).doesNotContain("/private-main", "/private-sub", "rtsp://");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT result_summary FROM camera_create_request WHERE resource_id=?",
                                String.class,
                                Long.valueOf(id)))
                .doesNotContain("/private-main", "rtsp://");
    }

    @Test
    void concurrentCreateRetriesCommitOneAssetAndReceipt() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        var command = cameraBody(sourceId, "并发重试", requestId());
        var start = new java.util.concurrent.CountDownLatch(1);
        try (var executor = java.util.concurrent.Executors.newFixedThreadPool(2)) {
            var first =
                    executor.submit(
                            () -> {
                                start.await();
                                return write(post("/cameras"), admin, command, 201);
                            });
            var second =
                    executor.submit(
                            () -> {
                                start.await();
                                return write(post("/cameras"), admin, command, 201);
                            });
            start.countDown();
            assertThat(first.get(15, java.util.concurrent.TimeUnit.SECONDS))
                    .isEqualTo(second.get(15, java.util.concurrent.TimeUnit.SECONDS));
        }
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_create_request WHERE operation='CREATE_CAMERA'",
                                Integer.class))
                .isOne();
    }

    @Test
    void rollsBackAllProfilesAndReceiptIfALocatorFails() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        var command = new LinkedHashMap<>(cameraBody(sourceId, "事务测试", requestId()));
        command.put(
                "profiles",
                List.of(
                        profileBody("main", "MAIN", "/valid"),
                        profileBody("bad", "SUB", "not-an-absolute-path")));
        write(post("/cameras"), admin, command, 400);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_stream_profile", Integer.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_profile_locator", Integer.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_create_request WHERE operation='CREATE_CAMERA'",
                                Integer.class))
                .isZero();
    }

    @Test
    void protectsDefaultMembershipVersionAndExplicitDisableReplacement() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        JsonNode first = camera(admin, sourceId, "一路");
        JsonNode second = camera(admin, sourceId, "二路");
        String id = first.path("cameraId").asText();
        String main = first.path("defaultPreviewProfileId").asText();
        String sub = first.path("profiles").get(1).path("streamProfileId").asText();
        String foreign = second.path("defaultPreviewProfileId").asText();
        write(
                put("/cameras/" + id),
                admin,
                Map.of("version", "0", "defaultPreviewProfileId", foreign),
                404);
        assertThatThrownBy(
                        () ->
                                jdbc.update(
                                        "UPDATE camera_channel SET default_preview_profile_id=? WHERE id=?",
                                        Long.valueOf(foreign),
                                        Long.valueOf(id)))
                .isInstanceOf(DataIntegrityViolationException.class);
        mvc.perform(
                        delete("/cameras/" + id + "/profiles/" + main)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.code").value("CAMERA_REFERENCED"));
        write(
                put("/cameras/" + id + "/profiles/" + main),
                admin,
                Map.of("version", "0", "enabled", false),
                400);
        JsonNode disabled =
                write(
                        put("/cameras/" + id + "/profiles/" + main),
                        admin,
                        Map.of(
                                "version",
                                "0",
                                "enabled",
                                false,
                                "cameraVersion",
                                "0",
                                "replacementDefaultProfileId",
                                sub),
                        200);
        assertThat(disabled.path("enabled").asBoolean()).isFalse();
        JsonNode current = read("/cameras/" + id, admin);
        assertThat(current.path("defaultPreviewProfileId").asText()).isEqualTo(sub);
        assertThat(current.path("version").asText()).isEqualTo("1");
        write(put("/cameras/" + id), admin, Map.of("version", "0", "name", "旧表单"), 409);
        mvc.perform(
                        delete("/cameras/" + id + "/profiles/" + main)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isPreconditionFailed());
        mvc.perform(
                        delete("/cameras/" + id + "/profiles/" + main)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"1\""))
                .andExpect(status().isOk());
        assertThat(read("/cameras/" + id + "/profiles", admin)).hasSize(1);
    }

    @Test
    void scopesListsCountsProfilesAndRejectsOrdinarySensitiveFields() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        String visibleId = camera(admin, sourceId, "许可相机").path("cameraId").asText();
        String hiddenId = camera(admin, sourceId, "隐藏相机").path("cameraId").asText();
        String group =
                write(post("/camera-groups"), admin, Map.of("name", "作业区域"), 201)
                        .path("groupId")
                        .asText();
        archive(admin, visibleId, group);
        archive(admin, hiddenId, group);
        String userId =
                write(post("/user"), admin, Map.of("username", "CameraWorker"), 201)
                        .path("id")
                        .asText();
        // Fixture only: make the newly-created test account normal and grant only the camera PAGE.
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8901,'CAMERA_TEST','相机测试','ENABLED',0)");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8901)", Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8901,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        write(
                put("/camera-scopes/users/" + userId),
                admin,
                Map.of("version", "0", "groupIds", List.of(), "cameraIds", List.of(visibleId)),
                200);
        Csrf worker = login("CameraWorker", "Initial1!");
        JsonNode list = read("/cameras/page?sourceId=" + sourceId, worker);
        assertThat(list.path("total").asLong()).isOne();
        assertThat(list.path("items")).hasSize(1);
        assertThat(list.path("items").get(0).path("cameraId").asText()).isEqualTo(visibleId);
        assertThat(list.path("items").get(0).has("sourceId")).isFalse();
        assertThat(list.path("items").get(0).has("externalChannelKey")).isFalse();
        mvc.perform(get("/cameras/" + hiddenId).session(worker.session()))
                .andExpect(status().isNotFound());
        mvc.perform(get("/cameras/" + hiddenId + "/profiles").session(worker.session()))
                .andExpect(status().isNotFound());
        write(put("/cameras/" + visibleId), worker, Map.of("version", "1", "name", "许可的新名称"), 200);
        var forbidden = new LinkedHashMap<String, Object>();
        forbidden.put("version", "2");
        forbidden.put("groupId", null);
        write(put("/cameras/" + visibleId), worker, forbidden, 403);
        write(post("/cameras"), worker, cameraBody(sourceId, "不允许创建", requestId()), 403);
        mvc.perform(
                        delete("/cameras/" + visibleId)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"2\""))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.data.references[0].type").value("DIRECT_CAMERA_GRANT"));
        write(
                put("/camera-scopes/users/" + userId),
                admin,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of()),
                200);
        assertThat(read("/cameras/page", worker).path("total").asLong()).isZero();
        mvc.perform(get("/cameras/" + visibleId).session(worker.session()))
                .andExpect(status().isNotFound());
    }

    @Test
    void deletesOwnedRowsAtomicallyAndCannotReplayDeletedAsset() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        var command = cameraBody(sourceId, "删除测试", requestId());
        String id = write(post("/cameras"), admin, command, 201).path("cameraId").asText();
        mvc.perform(
                        delete("/cameras/" + id)
                                .session(admin.session())
                                .header(admin.header(), admin.token()))
                .andExpect(status().isPreconditionRequired());
        mvc.perform(
                        delete("/cameras/" + id)
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_stream_profile", Integer.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_profile_locator", Integer.class))
                .isZero();
        write(post("/cameras"), admin, command, 404);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isZero();
    }

    @Test
    void addsProfilesIdempotentlyAndAllowsExplicitDefaultClearing() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        JsonNode camera = camera(admin, sourceId, "扩展码流");
        String id = camera.path("cameraId").asText();
        var command =
                Map.of(
                        "cameraVersion",
                        "0",
                        "clientRequestId",
                        requestId(),
                        "label",
                        "第三码流",
                        "usageHint",
                        "THIRD",
                        "enabled",
                        true,
                        "locatorKind",
                        "RTSP",
                        "locator",
                        locatorBody("/private-third"));
        JsonNode added = write(post("/cameras/" + id + "/profiles"), admin, command, 201);
        assertThat(write(post("/cameras/" + id + "/profiles"), admin, command, 201))
                .isEqualTo(added);
        assertThat(read("/cameras/" + id + "/profiles", admin)).hasSize(3);
        var clear = new LinkedHashMap<String, Object>();
        clear.put("version", "0");
        clear.put("defaultPreviewProfileId", null);
        JsonNode changed = write(put("/cameras/" + id), admin, clear, 200);
        assertThat(changed.path("version").asText()).isEqualTo("1");
        assertThat(changed.path("defaultPreviewProfileId").isMissingNode()).isTrue();
        // Replay still resolves the original child after its parent version has changed.
        assertThat(write(post("/cameras/" + id + "/profiles"), admin, command, 201))
                .isEqualTo(added);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_stream_profile WHERE channel_id=?",
                                Integer.class,
                                Long.valueOf(id)))
                .isEqualTo(3);
    }

    @Test
    void keepsDeviceChannelAndProfileIdentitiesIndependent() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        JsonNode first = camera(admin, sourceId, "NVR通道一");
        JsonNode second = camera(admin, sourceId, "NVR通道二");
        // Device observation is a fixture here; this test does not claim a vendor was contacted.
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,device_type,manufacturer,model) VALUES(8801,?,'device-A','NVR','Observed vendor','Observed model')",
                Long.valueOf(sourceId));
        jdbc.update(
                "UPDATE camera_channel SET device_id=8801 WHERE source_id=?",
                Long.valueOf(sourceId));
        assertThat(
                        read("/cameras/" + first.path("cameraId").asText(), admin)
                                .path("deviceSummary")
                                .path("model")
                                .asText())
                .isEqualTo("Observed model");
        assertThat(read("/cameras/" + second.path("cameraId").asText() + "/profiles", admin))
                .hasSize(2);
        assertThat(read("/camera-devices/8801", admin).path("deviceType").asText())
                .isEqualTo("NVR");
        mvc.perform(
                        delete("/camera-devices/8801")
                                .session(admin.session())
                                .header(admin.header(), admin.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict())
                .andExpect(jsonPath("$.data.references[0].count").value(2));
        String secondSource = source(admin).path("sourceId").asText();
        String otherCamera = camera(admin, secondSource, "另一来源").path("cameraId").asText();
        assertThatThrownBy(
                        () ->
                                jdbc.update(
                                        "UPDATE camera_channel SET device_id=8801 WHERE id=?",
                                        Long.valueOf(otherCamera)))
                .isInstanceOf(DataIntegrityViolationException.class);
    }

    @Test
    void requiresCurrentImpactConfirmationForLifecycleChanges() throws Exception {
        Csrf admin = admin();
        String sourceId = source(admin).path("sourceId").asText();
        String id = camera(admin, sourceId, "启停测试").path("cameraId").asText();
        write(
                post("/cameras/" + id + "/lifecycle-preview"),
                admin,
                Map.of("cameraVersion", "0", "targetLifecycle", "DISABLED"),
                400);
        String group =
                write(post("/camera-groups"), admin, Map.of("name", "已归档区"), 201)
                        .path("groupId")
                        .asText();
        archive(admin, id, group);
        write(put("/cameras/" + id), admin, Map.of("version", "1", "lifecycle", "DISABLED"), 409);
        JsonNode preview =
                write(
                        post("/cameras/" + id + "/lifecycle-preview"),
                        admin,
                        Map.of("cameraVersion", "1", "targetLifecycle", "DISABLED"),
                        200);
        JsonNode disabled =
                write(
                        put("/cameras/" + id),
                        admin,
                        Map.of(
                                "version",
                                "1",
                                "lifecycle",
                                "DISABLED",
                                "confirmation",
                                preview.path("confirmation").asText()),
                        200);
        assertThat(disabled.path("lifecycle").asText()).isEqualTo("DISABLED");
        assertThat(disabled.path("version").asText()).isEqualTo("2");
        assertThat(read("/cameras/" + id + "/profiles", admin).get(0).path("enabled").asBoolean())
                .isTrue();
        assertThat(read("/camera-sources/" + sourceId, admin).path("enabled").asBoolean()).isTrue();
        JsonNode restore =
                write(
                        post("/cameras/" + id + "/lifecycle-preview"),
                        admin,
                        Map.of("cameraVersion", "2", "targetLifecycle", "ENABLED"),
                        200);
        assertThat(
                        write(
                                        put("/cameras/" + id),
                                        admin,
                                        Map.of(
                                                "version",
                                                "2",
                                                "lifecycle",
                                                "ENABLED",
                                                "confirmation",
                                                restore.path("confirmation").asText()),
                                        200)
                                .path("lifecycle")
                                .asText())
                .isEqualTo("ENABLED");
    }

    private void archive(Csrf auth, String cameraId, String groupId) throws Exception {
        JsonNode preview =
                write(
                        post("/cameras/" + cameraId + "/move-preview"),
                        auth,
                        Map.of("cameraVersion", "0", "targetGroupId", groupId),
                        200);
        write(
                put("/cameras/" + cameraId),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupId",
                        groupId,
                        "lifecycle",
                        "ENABLED",
                        "confirmation",
                        preview.path("confirmation").asText()),
                200);
    }
}
