package com.streamfusion.platform.audit.maintenance;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.support.CameraTestSupport;
import java.time.LocalDateTime;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;

class AuditMaintenanceIntegrationTest extends CameraTestSupport {
    @Autowired AuditRetentionService retention;
    @Autowired AuditExportService exports;
    @Autowired AuditExportStorage storage;

    private Map<String, Object> policy(boolean enabled, String mode, String version) {
        return Map.of(
                "enabled",
                enabled,
                "mode",
                mode,
                "keepMonths",
                6,
                "maxRecords",
                1000,
                "version",
                version);
    }

    private void historical(long id, LocalDateTime time) {
        jdbc.update(
                "INSERT INTO sys_operation_log(id,target_type,target_id,action,result,created_at) VALUES (?,'CAMERA',1,'CAMERA_UPDATE','SUCCESS',?)",
                id,
                time);
    }

    @Test
    void disabledPolicyNeverDeletesAndAgeCleanupPreservesBootstrap() throws Exception {
        var auth = admin();
        long boot =
                jdbc.queryForObject(
                        "SELECT id FROM sys_operation_log WHERE action='BOOTSTRAP'", Long.class);
        jdbc.update(
                "UPDATE sys_operation_log SET created_at=? WHERE id=?",
                LocalDateTime.now().minusYears(2),
                boot);
        historical(101, LocalDateTime.now().minusMonths(7));
        historical(102, LocalDateTime.now().minusMonths(5));
        retention.tick();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE id=101",
                                Integer.class))
                .isEqualTo(1);
        var preview = write(post("/audit/retention/preview"), auth, policy(true, "AGE", "0"), 200);
        assertThat(preview.path("candidates").asInt()).isEqualTo(1);
        assertThat(preview.path("protectedCount").asInt()).isEqualTo(1);
        var saved = write(put("/audit/retention"), auth, policy(true, "AGE", "0"), 200);
        write(
                post("/audit/retention/run"),
                auth,
                Map.of("version", saved.path("version").asText()),
                200);
        retention.tick();
        assertThat(
                        jdbc.queryForList(
                                "SELECT id FROM sys_operation_log WHERE id IN (101,102) ORDER BY id",
                                Long.class))
                .containsExactly(102L);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE id=?",
                                Integer.class,
                                boot))
                .isEqualTo(1);
        assertThat(read("/audit/retention", auth).path("lastResult").asText())
                .isEqualTo("COMPLETED");
        write(put("/audit/retention"), auth, policy(false, "AGE", "0"), 409);
        write(
                put("/audit/retention"),
                auth,
                policy(false, "AGE", saved.path("version").asText()),
                200);
        assertThat(read("/audit/retention", auth).path("nextAt").isNull()).isTrue();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT next_at FROM sys_audit_retention_policy WHERE id=1",
                                LocalDateTime.class))
                .isNull();
    }

    @Test
    void busyDownloadsRemainJsonErrorsAndCompletionClearsLease() throws Exception {
        var auth = admin();
        var job =
                write(
                        post("/audit/exports"),
                        auth,
                        Map.of("clientRequestId", UUID.randomUUID().toString(), "format", "CSV"),
                        201);
        assertThat(exports.workOne()).isTrue();
        long id = Long.parseLong(job.path("id").asText());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT lease_until FROM sys_audit_export WHERE id=?",
                                LocalDateTime.class,
                                id))
                .isNull();
        try (var file = storage.open(id)) {
            var busy =
                    mvc.perform(get("/audit/exports/" + id + "/download").session(auth.session()))
                            .andExpect(status().isTooManyRequests())
                            .andReturn()
                            .getResponse();
            assertThat(busy.getContentType()).startsWith("application/json");
            assertThat(busy.getHeader("Content-Disposition")).isNull();
            assertThat(json.readTree(busy.getContentAsByteArray()).path("code").asText())
                    .isEqualTo("RATE_LIMITED");
        }
        mvc.perform(get("/audit/exports/" + id + "/download").session(auth.session()))
                .andExpect(status().isOk());
    }

    @Test
    void countUsesStableOrderAndExportBlocksCleanup() throws Exception {
        var auth = admin();
        var at = LocalDateTime.now().minusDays(10).withNano(0);
        for (int i = 0; i < 1050; i++) historical(1000 + i, at);
        write(put("/audit/retention"), auth, policy(true, "COUNT", "0"), 200);
        var job =
                write(
                        post("/audit/exports"),
                        auth,
                        Map.of(
                                "clientRequestId",
                                UUID.randomUUID().toString(),
                                "format",
                                "JSONL",
                                "filter",
                                Map.of("module", "CAMERA")),
                        201);
        write(post("/audit/retention/run"), auth, Map.of("version", "1"), 200);
        retention.tick();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE target_type='CAMERA'",
                                Integer.class))
                .isEqualTo(1050);
        assertThat(exports.workOne()).isTrue();
        var completed = read("/audit/exports/" + job.path("id").asText(), auth);
        assertThat(completed.path("rowCount").asInt()).isEqualTo(1050);
        var download =
                mvc.perform(
                                get("/audit/exports/" + job.path("id").asText() + "/download")
                                        .session(auth.session()))
                        .andExpect(status().isOk())
                        .andReturn();
        assertThat(download.getResponse().getContentAsString().lines().count()).isEqualTo(1050);
        retention.tick();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_operation_log", Integer.class))
                .isLessThanOrEqualTo(1001);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='BOOTSTRAP'",
                                Integer.class))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE id=1000",
                                Integer.class))
                .isZero();
    }

    @Test
    void exportIsIdempotentBeyondPageSizeAndCanBeCancelledOrRemoved() throws Exception {
        var auth = admin();
        for (int i = 0; i < 130; i++) historical(100 + i, LocalDateTime.now().minusDays(2));
        var input =
                Map.of(
                        "clientRequestId",
                        UUID.randomUUID().toString(),
                        "format",
                        "CSV",
                        "filter",
                        Map.of("module", "CAMERA", "size", 1));
        var first = write(post("/audit/exports"), auth, input, 201);
        var replay = write(post("/audit/exports"), auth, input, 201);
        assertThat(replay.path("id").asText()).isEqualTo(first.path("id").asText());
        assertThat(exports.workOne()).isTrue();
        var download =
                mvc.perform(
                                get("/audit/exports/" + first.path("id").asText() + "/download")
                                        .session(auth.session()))
                        .andExpect(status().isOk())
                        .andReturn();
        assertThat(
                        download.getResponse()
                                .getContentAsString(java.nio.charset.StandardCharsets.UTF_8)
                                .lines()
                                .count())
                .isEqualTo(131);
        write(delete("/audit/exports/" + first.path("id").asText() + "/file"), auth, Map.of(), 200);
        mvc.perform(
                        get("/audit/exports/" + first.path("id").asText() + "/download")
                                .session(auth.session()))
                .andExpect(status().isConflict());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE target_type='CAMERA'",
                                Integer.class))
                .isEqualTo(130);
        var pending =
                write(
                        post("/audit/exports"),
                        auth,
                        Map.of("clientRequestId", UUID.randomUUID().toString(), "format", "CSV"),
                        201);
        write(
                post("/audit/exports/" + pending.path("id").asText() + "/cancel"),
                auth,
                Map.of(),
                200);
        assertThat(exports.workOne()).isFalse();
    }

    @Test
    void ordinaryAuditorCannotConfigureCleanupReadOtherExportsOrKeepExportingAfterRevocation()
            throws Exception {
        var auth = admin();
        jdbc.update(
                "INSERT INTO sys_user(id,username,password,nickname,status,must_change_password,session_version,version) SELECT 991,'AuditViewer',password,'viewer',1,FALSE,0,0 FROM sys_user WHERE username='CameraAdmin'");
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status) VALUES (992,'AUDIT_VIEWER','审计查看','ENABLED')");
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES (991,992)");
        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES (992,1006)");
        var viewer = login("AuditViewer", "AdminPass1!");
        mvc.perform(get("/audit/retention").session(viewer.session()))
                .andExpect(status().isForbidden());
        var administratorJob =
                write(
                        post("/audit/exports"),
                        auth,
                        Map.of("clientRequestId", UUID.randomUUID().toString(), "format", "CSV"),
                        201);
        mvc.perform(
                        get("/audit/exports/" + administratorJob.path("id").asText())
                                .session(viewer.session()))
                .andExpect(status().isNotFound());
        write(
                post("/audit/exports/" + administratorJob.path("id").asText() + "/cancel"),
                auth,
                Map.of(),
                200);
        var viewerJob =
                write(
                        post("/audit/exports"),
                        viewer,
                        Map.of("clientRequestId", UUID.randomUUID().toString(), "format", "CSV"),
                        201);
        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=992");
        assertThat(exports.workOne()).isTrue();
        JsonNode row = read("/audit/exports/" + viewerJob.path("id").asText(), auth);
        assertThat(row.path("state").asText()).isEqualTo("FAILED");
        assertThat(row.path("reasonCode").asText()).isEqualTo("EXPORT_PERMISSION_REVOKED");
        assertThat(row.path("downloadable").asBoolean()).isFalse();
    }
}
