package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class CameraGroupScopeIntegrationTest extends CameraTestSupport {
    @Test
    void rootUniquenessMoveConfirmationCyclesAndReferencesAreEnforced() throws Exception {
        Csrf auth = admin();
        String a = group(auth, "AreaA", null);
        String b = group(auth, "AreaB", null);
        write(post("/camera-groups"), auth, Map.of("name", "areaa"), 409);
        var invalidUpdate = new java.util.HashMap<String, Object>();
        invalidUpdate.put("version", "0");
        invalidUpdate.put("name", null);
        write(put("/camera-groups/" + a), auth, invalidUpdate, 400);
        invalidUpdate.remove("name");
        invalidUpdate.put("sortOrder", null);
        write(put("/camera-groups/" + a), auth, invalidUpdate, 400);
        write(post("/camera-groups"), auth, Map.of("name", "含\u0001控制字符"), 400);
        String child = group(auth, "工作区", a);
        JsonNode preview =
                write(
                        post("/camera-groups/" + child + "/move-preview"),
                        auth,
                        Map.of("version", "0", "targetParentId", b),
                        200);
        write(put("/camera-groups/" + b), auth, Map.of("version", "0", "name", "已变更目标"), 200);
        write(
                put("/camera-groups/" + child),
                auth,
                Map.of(
                        "version",
                        "0",
                        "parentId",
                        b,
                        "confirmation",
                        preview.path("confirmation").asText()),
                409);
        JsonNode fresh =
                write(
                        post("/camera-groups/" + child + "/move-preview"),
                        auth,
                        Map.of("version", "0", "targetParentId", b),
                        200);
        JsonNode moved =
                write(
                        put("/camera-groups/" + child),
                        auth,
                        Map.of(
                                "version",
                                "0",
                                "parentId",
                                b,
                                "confirmation",
                                fresh.path("confirmation").asText()),
                        200);
        assertThat(moved.path("parentId").asText()).isEqualTo(b);
        assertThat(moved.path("version").asText()).isEqualTo("1");
        write(
                post("/camera-groups/" + b + "/move-preview"),
                auth,
                Map.of("version", "1", "targetParentId", child),
                409);
        mvc.perform(
                        delete("/camera-groups/" + b)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"1\""))
                .andExpect(status().isConflict());
        assertThat(read("/camera-groups?parentId=" + b, auth).path("items")).hasSize(1);
    }

    @Test
    void scopeUpdatesRetainDisabledExistingGrantsButRejectNewDisabledGrants() throws Exception {
        Csrf auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        String first = camera(auth, sourceId, "既有授权").path("cameraId").asText();
        String second = camera(auth, sourceId, "未曾授权").path("cameraId").asText();
        String group = group(auth, "安全区域", null);
        archive(auth, first, group);
        archive(auth, second, group);
        String user =
                write(post("/user"), auth, Map.of("username", "ScopeWorker"), 201)
                        .path("id")
                        .asText();
        JsonNode saved =
                write(
                        put("/camera-scopes/users/" + user),
                        auth,
                        Map.of("version", "0", "groupIds", List.of(), "cameraIds", List.of(first)),
                        200);
        assertThat(saved.path("version").asText()).isEqualTo("1");
        assertThat(saved.path("updatedBy").asText()).isNotBlank();
        disable(auth, first);
        disable(auth, second);
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of(first, second)),
                400);
        JsonNode retained =
                write(
                        put("/camera-scopes/users/" + user),
                        auth,
                        Map.of(
                                "version",
                                "1",
                                "groupIds",
                                List.of(group),
                                "cameraIds",
                                List.of(first)),
                        200);
        assertThat(retained.path("cameraGrants").get(0).path("lifecycle").asText())
                .isEqualTo("DISABLED");
        assertThat(retained.path("effectiveSummary").path("enabledCameraCount").asLong()).isZero();
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of()),
                409);
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "2", "groupIds", List.of(group), "cameraIds", List.of()),
                200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_user_channel_grant WHERE user_id=?",
                                Integer.class,
                                Long.valueOf(user)))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_user_group_grant WHERE user_id=?",
                                Integer.class,
                                Long.valueOf(user)))
                .isOne();
    }

    @Test
    void authorizationChangeInvalidatesPreparedCameraAssignment() throws Exception {
        Csrf auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        String camera = camera(auth, sourceId, "待归档").path("cameraId").asText();
        String group = group(auth, "影响范围", null);
        String user =
                write(post("/user"), auth, Map.of("username", "FutureViewer"), 201)
                        .path("id")
                        .asText();
        JsonNode old =
                write(
                        post("/cameras/" + camera + "/move-preview"),
                        auth,
                        Map.of("cameraVersion", "0", "targetGroupId", group),
                        200);
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "0", "groupIds", List.of(group), "cameraIds", List.of()),
                200);
        write(
                put("/cameras/" + camera),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupId",
                        group,
                        "lifecycle",
                        "ENABLED",
                        "confirmation",
                        old.path("confirmation").asText()),
                409);
        assertThat(read("/cameras/" + camera, auth).path("lifecycle").asText())
                .isEqualTo("PENDING_ASSIGNMENT");
        JsonNode fresh =
                write(
                        post("/cameras/" + camera + "/move-preview"),
                        auth,
                        Map.of("cameraVersion", "0", "targetGroupId", group),
                        200);
        assertThat(fresh.path("authorizationImpact").path("gainedUserCount").asLong()).isOne();
        write(
                put("/cameras/" + camera),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupId",
                        group,
                        "lifecycle",
                        "ENABLED",
                        "confirmation",
                        fresh.path("confirmation").asText()),
                200);
    }

    @Test
    void groupMoveRejectsChangedCameraSetAndOptionsHonorDescendants() throws Exception {
        Csrf auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        String root = group(auth, "原区域", null);
        String child = group(auth, "子区域", root);
        String destination = group(auth, "目标区域", null);
        JsonNode preview =
                write(
                        post("/camera-groups/" + child + "/move-preview"),
                        auth,
                        Map.of("version", "0", "targetParentId", destination),
                        200);
        String camera = camera(auth, sourceId, "新增影响通道").path("cameraId").asText();
        archive(auth, camera, child);
        write(
                put("/camera-groups/" + child),
                auth,
                Map.of(
                        "version",
                        "0",
                        "parentId",
                        destination,
                        "confirmation",
                        preview.path("confirmation").asText()),
                409);
        assertThat(read("/camera-scopes/camera-options/page?groupId=" + root, auth).path("items"))
                .isEmpty();
        assertThat(
                        read(
                                        "/camera-scopes/camera-options/page?groupId="
                                                + root
                                                + "&includeDescendants=true",
                                        auth)
                                .path("items"))
                .hasSize(1);
        assertThat(read("/camera-groups/" + root, auth).path("visibleCameraCount").asLong())
                .isOne();
        assertThat(
                        read("/camera-groups?parentId=" + root, auth)
                                .path("items")
                                .get(0)
                                .path("visibleCameraCount")
                                .asLong())
                .isOne();
        JsonNode users = read("/camera-scopes/user-options", auth);
        assertThat(users.path("items").get(0).path("isSuperAdmin").asBoolean()).isTrue();
    }

    private String group(Csrf auth, String name, String parent) throws Exception {
        Object body =
                parent == null ? Map.of("name", name) : Map.of("name", name, "parentId", parent);
        return write(post("/camera-groups"), auth, body, 201).path("groupId").asText();
    }

    private void archive(Csrf auth, String camera, String group) throws Exception {
        JsonNode preview =
                write(
                        post("/cameras/" + camera + "/move-preview"),
                        auth,
                        Map.of("cameraVersion", "0", "targetGroupId", group),
                        200);
        write(
                put("/cameras/" + camera),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupId",
                        group,
                        "lifecycle",
                        "ENABLED",
                        "confirmation",
                        preview.path("confirmation").asText()),
                200);
    }

    private void disable(Csrf auth, String camera) throws Exception {
        JsonNode preview =
                write(
                        post("/cameras/" + camera + "/lifecycle-preview"),
                        auth,
                        Map.of("cameraVersion", "1", "targetLifecycle", "DISABLED"),
                        200);
        write(
                put("/cameras/" + camera),
                auth,
                Map.of(
                        "version",
                        "1",
                        "lifecycle",
                        "DISABLED",
                        "confirmation",
                        preview.path("confirmation").asText()),
                200);
    }
}
