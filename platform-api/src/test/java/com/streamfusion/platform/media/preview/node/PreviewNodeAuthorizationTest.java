package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.streamfusion.platform.support.CameraTestSupport;
import java.util.Map;
import org.junit.jupiter.api.Test;

class PreviewNodeAuthorizationTest extends CameraTestSupport {
    @org.junit.jupiter.api.io.TempDir java.nio.file.Path nodeDirectory;

    @Test
    void platformDrainPermissionAndUnknownOperationGateReachRealTlsFence() throws Exception {
        var auth = admin();
        try (var node =
                new com.streamfusion.previewnode.PreviewControlTestFixture(
                        nodeDirectory, "maintenance-a")) {
            jdbc.update(
                    "INSERT INTO preview_node(id,node_id,name,control_uri,certificate_sha256,token_path,media_address,mode,config_version,version) VALUES(1,'maintenance-a','A',?,?,?,'192.0.2.10','DRAINING',?,0)",
                    node.origin.toString(),
                    node.pin,
                    node.token.toString(),
                    "c".repeat(64));
            String permit = java.util.UUID.randomUUID().toString();
            var input = Map.of("action", "PREPARE", "permitId", permit, "version", 0);
            String operation = java.util.UUID.randomUUID().toString();
            jdbc.update(
                    "INSERT INTO preview_operation(operation_id,node_id,viewer_id,action,command_digest,deadline_at,state) VALUES(?,'maintenance-a',?,'CREATE',?,?,'UNKNOWN')",
                    operation,
                    java.util.UUID.randomUUID().toString(),
                    "d".repeat(64),
                    System.currentTimeMillis());
            assertThat(
                            write(
                                            post("/preview-node/nodes/maintenance-a/maintenance"),
                                            auth,
                                            input,
                                            200)
                                    .path("reasonCode")
                                    .asText())
                    .isEqualTo("NODE_DRAIN_REQUIRED");
            assertThat(node.service.maintenanceState().state()).isEqualTo("OPEN");
            jdbc.update(
                    "UPDATE preview_operation SET state='FAILED' WHERE operation_id=?", operation);
            var sealed =
                    write(post("/preview-node/nodes/maintenance-a/maintenance"), auth, input, 200);
            assertThat(sealed.path("maintenance").path("permitId").asText()).isEqualTo(permit);
            assertThat(node.service.snapshot().state()).isEqualTo("MAINTENANCE");
            assertThat(
                            read("/preview-node/nodes/maintenance-a/maintenance", auth)
                                    .path("maintenance")
                                    .path("state")
                                    .asText())
                    .isEqualTo("SEALED");
            var remote = read("/preview-node/nodes", auth).path("nodes").findValues("nodeId");
            assertThat(remote).isNotEmpty();
            write(
                    post("/preview-node/nodes/maintenance-a/maintenance"),
                    auth,
                    Map.of("action", "RELEASE", "permitId", permit, "version", 0),
                    200);
            assertThat(node.service.snapshot().state()).isEqualTo("READY");
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT mode FROM preview_node WHERE node_id='maintenance-a'",
                                    String.class))
                    .isEqualTo("DRAINING");
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT COUNT(*) FROM sys_operation_log WHERE action LIKE 'PREVIEW_MAINTENANCE_%'",
                                    Integer.class))
                    .isEqualTo(2);
        }
    }

    @Test
    void defaultsToLocalAndNeverExposesMachineSecrets() throws Exception {
        mvc.perform(get("/preview-node")).andExpect(status().isUnauthorized());
        var auth = admin();
        var view = read("/preview-node", auth);
        assertThat(view.path("mode").asText()).isEqualTo("LOCAL");
        assertThat(view.path("nodeId").isNull()).isTrue();
        assertThat(view.toString()).doesNotContain("token", "certificateSha256", "password");
        assertThat(read("/preview/capacity", auth).path("maxTranscodes").asInt()).isEqualTo(64);
        var rejected = write(post("/preview-node/register"), auth, Map.of(), 200);
        assertThat(rejected.path("applied").asBoolean()).isFalse();
        assertThat(rejected.path("reasonCode").asText()).isEqualTo("NODE_REGISTRATION_MISSING");
        assertThat(rejected.path("node").path("mode").asText()).isEqualTo("LOCAL");
        var direct =
                write(
                        post("/preview-node/connections"),
                        auth,
                        Map.of(
                                "nodeId",
                                "a",
                                "name",
                                "A",
                                "controlUri",
                                "https://127.0.0.1:9443",
                                "certificateSha256",
                                "a".repeat(64),
                                "mediaAddress",
                                "127.0.0.1"),
                        200);
        assertThat(direct.path("reasonCode").asText())
                .isEqualTo("NODE_CREDENTIAL_DIRECTORY_MISSING");
        var ops = read("/preview-node/ops-instances", auth);
        assertThat(ops.path("available").asBoolean()).isFalse();
        assertThat(ops.path("items")).isEmpty();
        var opsImport =
                write(
                        post("/preview-node/ops-import"),
                        auth,
                        Map.of("instanceId", "sample", "releaseId", "sample"),
                        200);
        assertThat(opsImport.path("reasonCode").asText()).isEqualTo("OPS_NOT_CONFIGURED");
    }

    @Test
    void servicePagePermissionDoesNotGrantNodeAdministration() throws Exception {
        var auth = admin();
        String user =
                write(post("/user"), auth, Map.of("username", "PreviewNodeReader"), 201)
                        .path("id")
                        .asText();
        jdbc.update("UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?", user);
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8907,'NODE_READER','节点只读','ENABLED',0)");
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8907)", user);
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8907,id FROM sys_menu WHERE type='PAGE' AND module_key='server'");
        var reader = login("PreviewNodeReader", "Initial1!");
        assertThat(read("/preview-node", reader).path("mode").asText()).isEqualTo("LOCAL");
        write(post("/preview-node/register"), reader, Map.of(), 403);
        write(post("/preview-node/connections"), reader, Map.of(), 403);
        write(post("/preview-node/ops-import"), reader, Map.of(), 403);
        write(post("/preview-node/nodes/a/ops-sync/check"), reader, Map.of(), 403);
        write(post("/preview-node/nodes/a/ops-sync"), reader, Map.of(), 403);
        write(post("/preview-node/nodes/a/ops-sync/check"), csrf(null), Map.of(), 401);
        mvc.perform(get("/preview-node/ops-instances")).andExpect(status().isUnauthorized());
        write(put("/preview-node/mode"), reader, Map.of("mode", "REMOTE", "version", 0), 403);
        write(post("/preview-node/reconcile"), reader, Map.of(), 403);
        write(
                post("/preview-node/nodes/a/maintenance"),
                reader,
                Map.of(
                        "action",
                        "PREPARE",
                        "permitId",
                        java.util.UUID.randomUUID().toString(),
                        "version",
                        0),
                403);
        write(put("/preview-node/nodes/local"), reader, Map.of(), 403);
        assertThat(read("/preview-node/nodes", reader).path("registrationOptions")).isEmpty();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM preview_node WHERE node_id<>'local'",
                                Integer.class))
                .isZero();
    }

    @Test
    void policyUsesSuperAdminAuthorizationVersionAndAuditWithoutChangingExistingViewers()
            throws Exception {
        var auth = admin();
        var node = read("/preview-node/nodes", auth).path("nodes").get(0);
        var body = json.createObjectNode();
        body.put("mode", "DRAINING");
        body.put("priority", 7);
        body.put("version", node.path("version").asLong());
        body.set("budget", node.path("budget"));
        assertThat(
                        write(put("/preview-node/nodes/local"), auth, body, 200)
                                .path("applied")
                                .asBoolean())
                .isTrue();
        var after = read("/preview-node/nodes", auth).path("nodes").get(0);
        assertThat(after.path("mode").asText()).isEqualTo("DRAINING");
        assertThat(after.path("priority").asInt()).isEqualTo(7);
        assertThat(after.path("budget").path("maxViewers").asInt()).isEqualTo(64);
        assertThat(
                        write(put("/preview-node/nodes/local"), auth, body, 200)
                                .path("reasonCode")
                                .asText())
                .isEqualTo("NODE_CONFIGURATION_CHANGED");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='PREVIEW_NODE_POLICY'",
                                Integer.class))
                .isOne();
    }
}
