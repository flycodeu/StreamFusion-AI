package com.streamfusion.platform.monitor.audit.maintenance;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.support.CameraTestSupport;
import java.time.LocalDateTime;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;

/** Real HTTP and SQL locks; a revocation must not be overtaken by a waiting maintenance write. */
class AuditMaintenanceAuthorizationIntegrationTest extends CameraTestSupport {
    @Autowired PlatformTransactionManager manager;

    @ParameterizedTest
    @ValueSource(strings = {"SAVE", "RUN", "EXPORT"})
    void pendingMaintenanceWriteRechecksPermissionAfterConcurrentRevocationCommits(String operation)
            throws Exception {
        admin();
        jdbc.update(
                "INSERT INTO sys_user(id,username,password,nickname,status,must_change_password,session_version,version) SELECT 991,'AuditOperator',password,'operator',1,FALSE,0,0 FROM sys_user WHERE username='CameraAdmin'");
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status) VALUES (992,'AUDIT_OPERATOR','审计操作','ENABLED')");
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES (991,992)");
        jdbc.update("INSERT INTO sys_role_menu(role_id,menu_id) VALUES (992,1006)");
        long superRole =
                jdbc.queryForObject("SELECT id FROM sys_role WHERE code='SUPER_ADMIN'", Long.class);
        if (!"EXPORT".equals(operation))
            jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES (991,?)", superRole);
        var actor = login("AuditOperator", "AdminPass1!");
        if ("RUN".equals(operation))
            jdbc.update(
                    "UPDATE sys_audit_retention_policy SET enabled=TRUE,next_at=? WHERE id=1",
                    LocalDateTime.now().plusDays(1));
        var nextAt =
                jdbc.queryForObject(
                        "SELECT next_at FROM sys_audit_retention_policy WHERE id=1",
                        LocalDateTime.class);
        var request =
                "SAVE".equals(operation)
                        ? put("/audit/retention")
                        : post(
                                "EXPORT".equals(operation)
                                        ? "/audit/exports"
                                        : "/audit/retention/run");
        Map<String, Object> body =
                switch (operation) {
                    case "SAVE" ->
                            Map.of(
                                    "enabled",
                                    true,
                                    "mode",
                                    "COUNT",
                                    "keepMonths",
                                    6,
                                    "maxRecords",
                                    1000,
                                    "version",
                                    "0");
                    case "RUN" -> Map.of("version", "0");
                    default ->
                            Map.of(
                                    "clientRequestId",
                                    UUID.randomUUID().toString(),
                                    "format",
                                    "CSV");
                };
        var pending = new AtomicReference<Future<JsonNode>>();
        var executor = java.util.concurrent.Executors.newSingleThreadExecutor();
        try {
            var revocation = new TransactionTemplate(manager);
            revocation.setIsolationLevel(TransactionDefinition.ISOLATION_READ_COMMITTED);
            revocation.executeWithoutResult(
                    tx -> {
                        jdbc.queryForObject(
                                "SELECT id FROM sys_role WHERE id=? FOR UPDATE",
                                Long.class,
                                superRole);
                        jdbc.queryForObject(
                                "SELECT id FROM sys_audit_retention_policy WHERE id=1 FOR UPDATE",
                                Long.class);
                        int blockingSession =
                                jdbc.queryForObject("SELECT SESSION_ID()", Integer.class);
                        if ("EXPORT".equals(operation))
                            jdbc.update("DELETE FROM sys_role_menu WHERE role_id=992");
                        else
                            jdbc.update(
                                    "DELETE FROM sys_user_role WHERE user_id=991 AND role_id=?",
                                    superRole);
                        pending.set(executor.submit(() -> write(request, actor, body, 403)));
                        awaitDatabaseLock(blockingSession, pending.get());
                        // The caller is genuinely waiting on this transaction before revocation
                        // commits.
                    });
            pending.get().get(5, TimeUnit.SECONDS);
        } finally {
            executor.shutdownNow();
            assertThat(executor.awaitTermination(5, TimeUnit.SECONDS)).isTrue();
        }

        assertThat(
                        jdbc.queryForObject(
                                "SELECT version FROM sys_audit_retention_policy WHERE id=1",
                                Long.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT next_at FROM sys_audit_retention_policy WHERE id=1",
                                LocalDateTime.class))
                .isEqualTo(nextAt);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_audit_export", Integer.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action IN ('AUDIT_RETENTION_UPDATE','AUDIT_RETENTION_RUN','AUDIT_EXPORT_CREATE')",
                                Integer.class))
                .isZero();
    }

    private void awaitDatabaseLock(int blockingSession, Future<?> request) {
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        while (System.nanoTime() < deadline) {
            int waiting =
                    jdbc.queryForObject(
                            "SELECT COUNT(*) FROM INFORMATION_SCHEMA.SESSIONS WHERE BLOCKER_ID=?",
                            Integer.class,
                            blockingSession);
            if (waiting > 0) return;
            assertThat(request.isDone())
                    .as("request must wait for the real database lock")
                    .isFalse();
            java.util.concurrent.locks.LockSupport.parkNanos(TimeUnit.MILLISECONDS.toNanos(10));
        }
        throw new AssertionError("Maintenance request did not wait on the held database locks");
    }
}
