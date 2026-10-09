package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.streamfusion.platform.support.CameraTestSupport;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class CameraDeviceMoveIntegrationTest extends CameraTestSupport {
    private String group(Csrf auth, String name) throws Exception {
        return write(post("/camera-groups"), auth, Map.of("name", name), 201)
                .path("groupId")
                .asText();
    }

    private String device(Csrf auth, int count) throws Exception {
        String source = source(auth).path("sourceId").asText();
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,source_name,device_type,version,created_at,updated_at) VALUES(701,?,'device-701','双通道设备','IPC',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                source);
        for (int i = 1; i <= count; i++)
            jdbc.update(
                    "INSERT INTO camera_channel(id,source_id,device_id,external_channel_key,name,lifecycle,mapping_origin,version,created_at,updated_at) VALUES(?,?,701,?,?,'PENDING_ASSIGNMENT','MANUAL',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                    800 + i,
                    source,
                    "channel-" + i,
                    "通道" + i);
        return "d701";
    }

    private String preview(Csrf auth, String key, String target) throws Exception {
        return write(
                        post("/cameras/devices/" + key + "/move-preview"),
                        auth,
                        Map.of("targetGroupId", target),
                        200)
                .path("impact")
                .path("confirmation")
                .asText();
    }

    private void move(Csrf auth, String key, String target, String token, int status)
            throws Exception {
        write(
                post("/cameras/devices/" + key + "/move"),
                auth,
                Map.of("targetGroupId", target, "confirmation", token),
                status);
    }

    @Test
    void movesAllChannelsBeyondPaginationAndKeepsDisabledState() throws Exception {
        var auth = admin();
        String key = device(auth, 25), old = group(auth, "原区域"), target = group(auth, "目标区域");
        jdbc.update("UPDATE camera_channel SET group_id=?,lifecycle='DISABLED' WHERE id=801", old);
        var preview =
                write(
                        post("/cameras/devices/" + key + "/move-preview"),
                        auth,
                        Map.of("targetGroupId", target),
                        200);
        assertThat(preview.path("placements")).hasSize(2);
        assertThat(preview.path("impact").path("affectedCameraCount").asInt()).isEqualTo(25);
        move(auth, key, target, preview.path("impact").path("confirmation").asText(), 200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_channel WHERE group_id=? AND version=1",
                                Integer.class,
                                target))
                .isEqualTo(25);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT lifecycle FROM camera_channel WHERE id=801", String.class))
                .isEqualTo("DISABLED");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_channel WHERE lifecycle='ENABLED'",
                                Integer.class))
                .isEqualTo(24);
        // A repeated old confirmation never writes a new version.
        move(auth, key, target, preview.path("impact").path("confirmation").asText(), 409);
        assertThat(jdbc.queryForObject("SELECT MAX(version) FROM camera_channel", Integer.class))
                .isEqualTo(1);
    }

    @Test
    void memberAndVersionAndTreeChangesInvalidateConfirmation() throws Exception {
        var auth = admin();
        String key = device(auth, 2), target = group(auth, "目标");
        String token = preview(auth, key, target);
        jdbc.update("UPDATE camera_channel SET device_id=NULL WHERE id=802");
        move(auth, key, target, token, 409);
        jdbc.update("UPDATE camera_channel SET device_id=701 WHERE id=802");
        token = preview(auth, key, target);
        jdbc.update("UPDATE camera_channel SET version=1 WHERE id=802");
        move(auth, key, target, token, 409);
        token = preview(auth, key, target);
        write(put("/camera-groups/" + target), auth, Map.of("version", "0", "name", "新目标名称"), 200);
        move(auth, key, target, token, 409);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_channel WHERE group_id IS NOT NULL",
                                Integer.class))
                .isZero();
    }

    @Test
    void databaseFailureOnSecondChannelRollsBackFirstAndAudit() throws Exception {
        var auth = admin();
        String key = device(auth, 2), target = group(auth, "目标");
        String token = preview(auth, key, target);
        jdbc.execute(
                "ALTER TABLE camera_channel ADD CONSTRAINT test_device_move_failure CHECK(id <> 802 OR group_id IS NULL)");
        try {
            move(auth, key, target, token, 500);
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT COUNT(*) FROM camera_channel WHERE group_id IS NOT NULL OR version<>0",
                                    Integer.class))
                    .isZero();
        } finally {
            jdbc.execute("ALTER TABLE camera_channel DROP CONSTRAINT test_device_move_failure");
        }
    }

    @Test
    void unidentifiedRecordIsNotExpandedToOtherChannelsOnSameSource() throws Exception {
        var auth = admin();
        device(auth, 2);
        jdbc.update("UPDATE camera_channel SET device_id=NULL");
        String target = group(auth, "目标"), key = "c801";
        move(auth, key, target, preview(auth, key, target), 200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_channel WHERE group_id=?",
                                Integer.class,
                                target))
                .isOne();
        write(
                post("/cameras/devices/d701/move-preview"),
                auth,
                Map.of("targetGroupId", target),
                404);
    }

    @Test
    void authorizationChangesAndAnotherSessionCannotReuseConfirmation() throws Exception {
        var auth = admin();
        String key = device(auth, 2), target = group(auth, "目标");
        String user =
                write(post("/user"), auth, Map.of("username", "MoveViewer"), 201)
                        .path("id")
                        .asText();
        String token = preview(auth, key, target);
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "0", "groupIds", List.of(target), "cameraIds", List.of()),
                200);
        move(auth, key, target, token, 409);
        token = preview(auth, key, target);
        var other = login("CameraAdmin", "AdminPass1!");
        move(other, key, target, token, 409);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_channel WHERE group_id IS NOT NULL",
                                Integer.class))
                .isZero();
    }

    @Test
    void ordinaryAccountSeesOnlyAuthorizedTreeAncestorsAndCannotMoveDevice() throws Exception {
        var auth = admin();
        device(auth, 2);
        String root = group(auth, "厂区"), hidden = group(auth, "隐藏区域");
        String child =
                write(post("/camera-groups"), auth, Map.of("name", "车间", "parentId", root), 201)
                        .path("groupId")
                        .asText();
        jdbc.update("UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=801", child);
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=802", hidden);
        String user =
                write(post("/user"), auth, Map.of("username", "TreeViewer"), 201)
                        .path("id")
                        .asText();
        jdbc.update("UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?", user);
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8901,'CAMERA_TEST','相机测试','ENABLED',0)");
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8901)", user);
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8901,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "0", "groupIds", List.of(), "cameraIds", List.of("801")),
                200);
        var worker = login("TreeViewer", "Initial1!");
        var tree = read("/camera-groups/tree", worker);
        assertThat(tree).hasSize(2);
        for (var node : tree) {
            assertThat(node.path("groupId").asText()).isIn(root, child);
            assertThat(node.path("visibleCameraCount").asInt()).isOne();
        }
        write(
                post("/cameras/devices/d701/move-preview"),
                worker,
                Map.of("targetGroupId", hidden),
                403);
        move(worker, "d701", hidden, preview(auth, "d701", hidden), 403);
    }

    @Test
    void treeReturnsAllLevelsWithChannelCounts() throws Exception {
        var auth = admin();
        device(auth, 2);
        String parent = group(auth, "厂区");
        String child =
                write(post("/camera-groups"), auth, Map.of("name", "车间", "parentId", parent), 201)
                        .path("groupId")
                        .asText();
        move(auth, "d701", child, preview(auth, "d701", child), 200);
        var tree = read("/camera-groups/tree", auth);
        assertThat(tree).hasSize(2);
        for (var node : tree) assertThat(node.path("visibleCameraCount").asInt()).isEqualTo(2);
    }
}
