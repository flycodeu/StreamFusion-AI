package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.mapper.CameraGroupMapper;
import com.streamfusion.platform.camera.mapper.CameraScopeMapper;
import com.streamfusion.platform.camera.service.CameraGroupService;
import com.streamfusion.platform.camera.status.CameraStatusWorker;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.List;
import java.util.Map;
import java.util.stream.StreamSupport;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;

/** Request-local reuse only: real isolated SQL and unchanged scope/ancestor/count semantics. */
class CameraReadSnapshotIntegrationTest extends CameraTestSupport {
    @MockitoSpyBean private CameraGroupMapper groupMapper;
    @MockitoSpyBean private CameraScopeMapper scopeMapper;
    @MockitoBean private CameraStatusWorker worker;
    @Autowired private CameraGroupService groups;

    @Test
    void ordinaryTreeSharesOneTreeAndCountSnapshotWithoutExposingEmptyOrHiddenGroups()
            throws Exception {
        var f = fixture();
        clearInvocations(groupMapper, scopeMapper);
        var tree = read("/camera-groups/tree", f.ordinary());
        assertThat(tree).hasSize(3);
        assertCount(tree, f.root(), 2);
        assertCount(tree, f.child(), 2);
        assertCount(tree, f.direct(), 1);
        assertThat(row(tree, f.root()).path("hasChildren").asBoolean()).isTrue();
        assertThat(tree.toString()).doesNotContain("hidden-group", "empty-granted");
        verifyGroupReadOnce();
        verifyNoInteractions(worker);
    }

    @Test
    void ordinaryGroupPageKeepsAncestorCountsAndDisabledChannelsWithoutRepeatedScopeScans()
            throws Exception {
        var f = fixture();
        clearInvocations(groupMapper, scopeMapper);
        var page = read("/camera-groups", f.ordinary());
        assertThat(page.path("total").asInt()).isEqualTo(2);
        assertCount(page.path("items"), f.root(), 2);
        assertCount(page.path("items"), f.direct(), 1);
        verifyGroupReadOnce();
    }

    @Test
    void superAdminStillSeesEmptyGroupsAndDevicePageReadsOrdinaryScopeOnlyOnce() throws Exception {
        var f = fixture();
        clearInvocations(groupMapper, scopeMapper);
        var tree = read("/camera-groups/tree", f.admin());
        assertThat(tree).hasSize(5);
        assertCount(tree, f.empty(), 0);
        verify(groupMapper).allBounded();
        verify(scopeMapper).visibleGroupCounts(any());
        verify(scopeMapper, never()).visibleGroups(any());
        verify(scopeMapper, never()).grantedGroups(anyLong());

        clearInvocations(groupMapper, scopeMapper);
        var page = read("/cameras/devices/page", f.ordinary());
        assertThat(page.path("total").asInt()).isEqualTo(3);
        // One scope tree plus one group-path tree, not one scope tree per downstream reader.
        verify(groupMapper, times(2)).allBounded();
        verify(scopeMapper).grantedGroups(anyLong());
        verifyNoInteractions(worker);
    }

    @Test
    void multipleGroupRootsUseOneTreeAndPreserveSingleRootOrderAndMissingRootBehavior()
            throws Exception {
        var f = fixture();
        clearInvocations(groupMapper);
        assertThat(groups.queryGroupIds(List.of(f.root(), f.child(), 99L, f.direct()), true))
                .containsExactly(f.root(), f.child(), f.direct());
        verify(groupMapper).allBounded();
        clearInvocations(groupMapper);
        assertThat(groups.queryGroupIds(List.of(), true)).isEmpty();
        verifyNoInteractions(groupMapper);
        assertThat(groups.queryGroupIds(99L, true)).isEmpty();
        assertThat(groups.queryGroupIds(List.of(f.child(), f.root(), f.child()), false))
                .containsExactly(f.child(), f.root());
    }

    private void verifyGroupReadOnce() {
        verify(groupMapper).allBounded();
        verify(scopeMapper).grantedGroups(anyLong());
        verify(scopeMapper).visibleGroupCounts(any());
        verify(scopeMapper, never()).visibleGroups(any());
    }

    private Fixture fixture() throws Exception {
        var admin = admin();
        long root = group(admin, "root", null);
        long child = group(admin, "child", root);
        long direct = group(admin, "direct-group", null);
        long hidden = group(admin, "hidden-group", null);
        long empty = group(admin, "empty-granted", null);
        String source = source(admin).path("sourceId").asText();
        placed(admin, source, "enabled", child, "ENABLED");
        placed(admin, source, "disabled", child, "DISABLED");
        var directCamera = placed(admin, source, "direct", direct, "ENABLED");
        placed(admin, source, "hidden", hidden, "ENABLED");
        camera(admin, source, "pending-not-visible");
        String user =
                write(post("/user"), admin, Map.of("username", "ReadSnapshotUser"), 201)
                        .path("id")
                        .asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.valueOf(user));
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8831,'READ_SNAPSHOT','Read snapshot','ENABLED',0)");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8831)", Long.valueOf(user));
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8831,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        write(
                put("/camera-scopes/users/" + user),
                admin,
                Map.of(
                        "version", "0",
                        "groupIds", List.of(Long.toString(root), Long.toString(empty)),
                        "cameraIds", List.of(directCamera)),
                200);
        return new Fixture(
                admin, login("ReadSnapshotUser", "Initial1!"), root, child, direct, empty);
    }

    private String placed(Csrf auth, String source, String name, long group, String lifecycle)
            throws Exception {
        String id = camera(auth, source, name).path("cameraId").asText();
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle=? WHERE id=?",
                group,
                lifecycle,
                Long.valueOf(id));
        return id;
    }

    private long group(Csrf auth, String name, Long parent) throws Exception {
        var body = new java.util.HashMap<String, Object>();
        body.put("name", name);
        body.put("parentId", parent == null ? null : parent.toString());
        return write(post("/camera-groups"), auth, body, 201).path("groupId").asLong();
    }

    private static JsonNode row(JsonNode rows, long group) {
        return StreamSupport.stream(rows.spliterator(), false)
                .filter(row -> row.path("groupId").asLong() == group)
                .findFirst()
                .orElseThrow();
    }

    private static void assertCount(JsonNode rows, long group, long expected) {
        assertThat(row(rows, group).path("visibleCameraCount").asLong()).isEqualTo(expected);
    }

    private record Fixture(
            Csrf admin, Csrf ordinary, long root, long child, long direct, long empty) {}
}
