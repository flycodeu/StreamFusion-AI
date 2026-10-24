package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.support.CameraTestSupport;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.core.io.ByteArrayResource;
import org.springframework.core.io.support.EncodedResource;
import org.springframework.jdbc.datasource.init.ScriptUtils;

/** Device grants remain independent of stream identities and preserve the pending boundary. */
class CameraDeviceScopeIntegrationTest extends CameraTestSupport {
    @Test
    void sixteenChannelsCountAsOneDeviceAcrossSiblingGroupsAndCandidatePages() throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String root = group(auth, "根组", null),
                left = group(auth, "左组", root),
                right = group(auth, "右组", root);
        device(source, 7000);
        for (int index = 0; index < 16; index++)
            channel(auth, source, 7000L, index < 8 ? left : right, "画面 " + index);
        var summary = read("/camera-groups/" + root, auth);
        assertThat(summary.path("visibleCameraCount").asInt()).isEqualTo(16);
        assertThat(summary.path("visibleDeviceCount").asInt()).isOne();
        assertThat(read("/camera-groups/" + left, auth).path("visibleDeviceCount").asInt()).isOne();
        var candidates = read("/camera-scopes/camera-options/page?size=1", auth);
        assertThat(candidates.path("total").asInt()).isOne();
        assertThat(candidates.path("items")).hasSize(1);
        var candidate = candidates.path("items").get(0);
        assertThat(candidate.path("deviceId").asText()).isEqualTo("7000");
        assertThat(candidate.path("name").asText()).isEqualTo("录像机 7000");
        assertThat(candidate.path("groupIds").toString()).contains(left, right);
        assertThat(
                        read("/camera-scopes/camera-options/page?groupId=" + right, auth)
                                .path("total")
                                .asInt())
                .isOne();
        assertThat(read("/camera-scopes/camera-options/page?page=2&size=1", auth).path("items"))
                .isEmpty();
    }

    @Test
    void deviceGrantsCoverSiblingsAndFutureMembersButNotPendingOrOtherDevices() throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        String user = user(auth, "DeviceViewer");
        var saved = save(auth, user, "0", List.of(), List.of(), List.of("7000"));
        assertThat(saved.path("deviceIds").get(0).asText()).isEqualTo("7000");
        assertThat(saved.path("cameraIds")).isEmpty();
        assertThat(saved.path("deviceGrants").get(0).path("groupIds").toString())
                .contains(fixture.left(), fixture.right());
        assertThat(saved.path("effectiveSummary").path("effectiveCameraCount").asInt())
                .isEqualTo(2);
        assertThat(saved.path("effectiveSummary").path("effectiveDeviceCount").asInt()).isOne();
        var viewer = viewer(user, "DeviceViewer");
        assertThat(read("/cameras/page", viewer).path("total").asInt()).isEqualTo(2);
        hidden(viewer, fixture.pending());
        hidden(viewer, fixture.other());
        String future = channel(auth, fixture.source(), 7000L, fixture.left(), "未来成员");
        assertThat(read("/cameras/" + future, viewer).path("cameraId").asText()).isEqualTo(future);
        assertThat(read("/cameras/page", viewer).path("total").asInt()).isEqualTo(3);
        var reasons =
                read(
                        "/camera-scopes/users/"
                                + user
                                + "/cameras/"
                                + fixture.second()
                                + "/reasons",
                        auth);
        assertThat(reasons.path("direct").asBoolean()).isTrue();
    }

    @Test
    void groupGrantCoversWholeDeviceAndRecomputesWhenItsLastMemberMovesOut() throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        String user = user(auth, "GroupDeviceViewer");
        save(auth, user, "0", List.of(fixture.left()), List.of(), List.of());
        var viewer = viewer(user, "GroupDeviceViewer");
        assertThat(read("/cameras/" + fixture.second(), viewer).path("cameraId").asText())
                .isEqualTo(fixture.second());
        var reason =
                read(
                        "/camera-scopes/users/"
                                + user
                                + "/cameras/"
                                + fixture.second()
                                + "/reasons",
                        auth);
        assertThat(reason.path("direct").asBoolean()).isFalse();
        assertThat(reason.path("groups")).hasSize(1);
        jdbc.update(
                "UPDATE camera_channel SET group_id=? WHERE id=?",
                Long.valueOf(fixture.right()),
                Long.valueOf(fixture.first()));
        hidden(viewer, fixture.second());
        hidden(viewer, fixture.first());
    }

    @Test
    void groupCandidatesIncludeAnEnabledDeviceWhoseMatchingGroupMemberIsDisabled()
            throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        jdbc.update(
                "UPDATE camera_channel SET lifecycle='DISABLED' WHERE id=?",
                Long.valueOf(fixture.second()));
        var candidates =
                read("/camera-scopes/camera-options/page?groupId=" + fixture.right(), auth);
        assertThat(candidates.path("total").asInt()).isOne();
        assertThat(candidates.path("items").get(0).path("deviceId").asText()).isEqualTo("7000");
        assertThat(candidates.path("items").get(0).path("lifecycle").asText()).isEqualTo("ENABLED");
    }

    @Test
    void legacyChannelGrantsReadAsDevicesWithoutWritesAndNormalizeOnExplicitSave()
            throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        String user = user(auth, "LegacyDeviceViewer");
        jdbc.update("INSERT INTO camera_user_scope(user_id) VALUES(?)", Long.valueOf(user));
        jdbc.update(
                "INSERT INTO camera_user_channel_grant(user_id,channel_id) VALUES(?,?)",
                Long.valueOf(user),
                Long.valueOf(fixture.first()));
        var loaded = read("/camera-scopes/users/" + user, auth);
        assertThat(loaded.path("deviceIds").get(0).asText()).isEqualTo("7000");
        assertThat(loaded.path("cameraIds")).isEmpty();
        assertThat(count("camera_user_device_grant", user)).isZero();
        var viewer = viewer(user, "LegacyDeviceViewer");
        assertThat(read("/cameras/" + fixture.second(), viewer).path("cameraId").asText())
                .isEqualTo(fixture.second());
        var normalized =
                write(
                        put("/camera-scopes/users/" + user),
                        auth,
                        Map.of("version", "0", "groupIds", List.of(), "cameraIds", List.of()),
                        200);
        assertThat(normalized.path("deviceIds").get(0).asText()).isEqualTo("7000");
        assertThat(count("camera_user_device_grant", user)).isOne();
        assertThat(count("camera_user_channel_grant", user)).isZero();
        var cleared = save(auth, user, "1", List.of(), List.of(), List.of());
        assertThat(cleared.path("deviceIds")).isEmpty();
        hidden(viewer, fixture.second());
    }

    @Test
    void fallbackAndDeviceIdsStaySeparateAndNewDisabledDevicesAreRejected() throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        String fallback = channel(auth, fixture.source(), null, fixture.left(), "独立RTSP");
        String user = user(auth, "MixedDeviceViewer");
        var saved =
                save(
                        auth,
                        user,
                        "0",
                        List.of(),
                        List.of(fallback, fixture.first(), fixture.second()),
                        List.of());
        assertThat(saved.path("cameraIds")).hasSize(1);
        assertThat(saved.path("cameraIds").get(0).asText()).isEqualTo(fallback);
        assertThat(saved.path("deviceIds")).hasSize(1);
        assertThat(saved.path("effectiveSummary").path("effectiveDeviceCount").asInt())
                .isEqualTo(2);
        jdbc.update(
                "UPDATE camera_channel SET lifecycle='DISABLED' WHERE device_id=7000 AND group_id IS NOT NULL");
        save(auth, user, "1", List.of(), List.of(fallback), List.of("7000"));
        String otherUser = user(auth, "DisabledDeviceViewer");
        write(
                put("/camera-scopes/users/" + otherUser),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupIds",
                        List.of(),
                        "cameraIds",
                        List.of(),
                        "deviceIds",
                        List.of("7000")),
                400);
    }

    @Test
    void deviceGrantReferencesBlockOrphaningAndUserDeletionCascades() throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText(), group = group(auth, "设备删除", null);
        device(source, 7000);
        String channel = channel(auth, source, 7000L, group, "最后画面");
        String user = user(auth, "ReferencedDeviceViewer");
        save(auth, user, "0", List.of(), List.of(), List.of("7000"));
        mvc.perform(
                        delete("/cameras/" + channel)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict());
        mvc.perform(
                        delete("/user/" + user)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        assertThat(count("camera_user_device_grant", user)).isZero();
        mvc.perform(
                        delete("/cameras/" + channel)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        mvc.perform(
                        delete("/camera-devices/7000")
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
    }

    @Test
    void upgradeBackfillsEachLegacyDeviceOnceAndPreservesEarliestProvenance() throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        String user = user(auth, "UpgradeDeviceViewer");
        jdbc.update("INSERT INTO camera_user_scope(user_id) VALUES(?)", Long.valueOf(user));
        jdbc.update(
                "INSERT INTO camera_user_channel_grant(user_id,channel_id,created_at,created_by) VALUES(?,?,'2026-10-01 01:00:00',123)",
                Long.valueOf(user),
                Long.valueOf(fixture.first()));
        jdbc.update(
                "INSERT INTO camera_user_channel_grant(user_id,channel_id,created_at,created_by) VALUES(?,?,'2026-10-02 01:00:00',456)",
                Long.valueOf(user),
                Long.valueOf(fixture.second()));
        jdbc.execute("DROP TABLE camera_user_device_grant");
        String upgrade =
                Files.readString(
                        Path.of("sql", "升级", "20261024-camera-device-grants.sql"),
                        StandardCharsets.UTF_8);
        try (var connection = dataSource.getConnection()) {
            ScriptUtils.executeSqlScript(
                    connection,
                    new EncodedResource(
                            new ByteArrayResource(upgrade.getBytes(StandardCharsets.UTF_8)),
                            StandardCharsets.UTF_8));
        }
        assertThat(count("camera_user_device_grant", user)).isOne();
        assertThat(count("camera_user_channel_grant", user)).isEqualTo(2);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT created_by FROM camera_user_device_grant WHERE user_id=?",
                                Long.class,
                                Long.valueOf(user)))
                .isEqualTo(123L);
    }

    @Test
    void movingOneGroupDoesNotReportPermissionLossWhenASiblingStillGrantsTheDevice()
            throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        String destination = group(auth, "另一区域", null), user = user(auth, "DeviceMoveViewer");
        save(auth, user, "0", List.of(fixture.right()), List.of(), List.of());
        var preview =
                write(
                        post("/camera-groups/" + fixture.left() + "/move-preview"),
                        auth,
                        Map.of("version", "0", "targetParentId", destination),
                        200);
        assertThat(preview.path("authorizationImpact").path("lostUserCount").asInt()).isZero();
        assertThat(preview.path("authorizationImpact").path("gainedUserCount").asInt()).isZero();
    }

    @Test
    void changingASiblingPlacementInvalidatesAPreparedChannelMove() throws Exception {
        var auth = admin();
        var fixture = fixture(auth);
        String destination = group(auth, "新位置", null), user = user(auth, "PreparedDeviceViewer");
        save(auth, user, "0", List.of(fixture.right()), List.of(), List.of());
        var preview =
                write(
                        post("/cameras/" + fixture.first() + "/move-preview"),
                        auth,
                        Map.of("cameraVersion", "0", "targetGroupId", destination),
                        200);
        jdbc.update(
                "UPDATE camera_channel SET group_id=? WHERE id=?",
                Long.valueOf(fixture.left()),
                Long.valueOf(fixture.second()));
        write(
                put("/cameras/" + fixture.first()),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupId",
                        destination,
                        "lifecycle",
                        "ENABLED",
                        "confirmation",
                        preview.path("confirmation").asText()),
                409);
    }

    private record Fixture(
            String source,
            String left,
            String right,
            String first,
            String second,
            String pending,
            String other) {}

    private Fixture fixture(Csrf auth) throws Exception {
        String source = source(auth).path("sourceId").asText(),
                left = group(auth, "厂区左", null),
                right = group(auth, "厂区右", null);
        device(source, 7000);
        device(source, 7001);
        String first = channel(auth, source, 7000L, left, "画面一"),
                second = channel(auth, source, 7000L, right, "画面二");
        String pending = channel(auth, source, 7000L, null, "待归档"),
                other = channel(auth, source, 7001L, left, "另一设备");
        return new Fixture(source, left, right, first, second, pending, other);
    }

    private void device(String source, long id) {
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,device_type,local_name) VALUES(?,?,?,'NVR',?)",
                id,
                Long.valueOf(source),
                "recorder-" + id,
                "录像机 " + id);
    }

    private String channel(Csrf auth, String source, Long device, String group, String name)
            throws Exception {
        String id = camera(auth, source, name).path("cameraId").asText();
        jdbc.update(
                "UPDATE camera_channel SET device_id=?,group_id=?,lifecycle=? WHERE id=?",
                device,
                group == null ? null : Long.valueOf(group),
                group == null ? "PENDING_ASSIGNMENT" : "ENABLED",
                Long.valueOf(id));
        return id;
    }

    private String group(Csrf auth, String name, String parent) throws Exception {
        return write(
                        post("/camera-groups"),
                        auth,
                        parent == null
                                ? Map.of("name", name)
                                : Map.of("name", name, "parentId", parent),
                        201)
                .path("groupId")
                .asText();
    }

    private String user(Csrf auth, String name) throws Exception {
        return write(post("/user"), auth, Map.of("username", name), 201).path("id").asText();
    }

    private Csrf viewer(String user, String name) throws Exception {
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.valueOf(user));
        jdbc.update("INSERT INTO sys_role(id,code,name) VALUES(9001,'DEVICE_VIEWER','设备查看')");
        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES(9001,1101)");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES(?,9001)", Long.valueOf(user));
        return login(name, "Initial1!");
    }

    private JsonNode save(
            Csrf auth,
            String user,
            String version,
            List<String> groups,
            List<String> cameras,
            List<String> devices)
            throws Exception {
        return write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of(
                        "version",
                        version,
                        "groupIds",
                        groups,
                        "cameraIds",
                        cameras,
                        "deviceIds",
                        devices),
                200);
    }

    private int count(String table, String user) {
        return jdbc.queryForObject(
                "SELECT COUNT(*) FROM " + table + " WHERE user_id=?",
                Integer.class,
                Long.valueOf(user));
    }

    private void hidden(Csrf viewer, String camera) throws Exception {
        mvc.perform(get("/cameras/" + camera).session(viewer.session()))
                .andExpect(status().isNotFound());
    }
}
