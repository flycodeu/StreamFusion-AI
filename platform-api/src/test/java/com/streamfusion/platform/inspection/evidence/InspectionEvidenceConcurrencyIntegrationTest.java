package com.streamfusion.platform.inspection.evidence;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.catchThrowable;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.storage.s3.StorageHttpTransport;
import com.streamfusion.platform.storage.s3.StorageS3Client;
import com.streamfusion.platform.support.CameraTestSupport;
import com.zaxxer.hikari.HikariDataSource;
import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneOffset;
import java.util.Base64;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.core.io.ByteArrayResource;
import org.springframework.core.io.support.EncodedResource;
import org.springframework.jdbc.datasource.init.ScriptUtils;
import org.springframework.test.context.TestPropertySource;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/** Isolated H2 race checks; MySQL remains the row-lock acceptance environment. */
@TestPropertySource(
        properties = {
            "platform.inspection.evidence.max-accounted-bytes-per-scope=1048576",
            "platform.inspection.evidence.node-tokens.test-node=inspection-concurrency-test-token-00000001",
            "platform.storage.allowed-endpoints=https://127.0.0.1:9443"
        })
class InspectionEvidenceConcurrencyIntegrationTest extends CameraTestSupport {
    @Autowired private InspectionEvidenceService service;
    @Autowired private PlatformTransactionManager transactions;
    @MockitoBean private StorageS3Client s3;
    private String planId;
    private String cameraId;
    private String profileId;
    private Csrf auth;

    @BeforeEach
    void fixture() throws Exception {
        createInspectionTables();
        auth = admin();
        JsonNode camera = camera(auth, source(auth).path("sourceId").asText(), "evidence-fixture");
        cameraId = camera.path("cameraId").asText();
        profileId = camera.path("profiles").get(0).path("streamProfileId").asText();
        String group =
                write(post("/camera-groups"), auth, Map.of("name", "evidence-test-group"), 201)
                        .path("groupId")
                        .asText();
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                Long.valueOf(group),
                Long.valueOf(cameraId));
        long owner =
                jdbc.queryForObject(
                        "SELECT id FROM sys_user WHERE username='CameraAdmin'", Long.class);
        planId = "901001";
        LocalDateTime now = LocalDateTime.ofInstant(Instant.now(), ZoneOffset.UTC);
        jdbc.update(
                "INSERT INTO camera_status_plan(id,actor_user_id,client_request_id,request_key_id,request_fingerprint,state,layer,check_video,dispatch_mode,parallelism,media_target_count,interval_minutes,target_count,start_at,end_at,created_at,updated_at,version) VALUES(?,?,?,'testkey',?,'ACTIVE','CONTROL',0,'POLLING',1,0,60,1,?,?,?, ?,0)",
                Long.valueOf(planId),
                owner,
                "evidence-test-request",
                new byte[32],
                java.sql.Timestamp.valueOf(now.minusMinutes(1)),
                java.sql.Timestamp.valueOf(now.plusHours(1)),
                java.sql.Timestamp.valueOf(now),
                java.sql.Timestamp.valueOf(now));
        String spec =
                json.writeValueAsString(
                        Map.of("planId", planId, "targetId", cameraId, "profileId", profileId));
        jdbc.update(
                "INSERT INTO inspection_control(control_id,plan_id,node_id,target_boot_id,control_seq,desired_state,expires_at,observed_state) VALUES('control-a',?,'test-node','boot-a',1,'RUNNING',?,'READY')",
                planId,
                java.sql.Timestamp.valueOf(now.plusHours(1)));
        jdbc.update(
                "INSERT INTO inspection_job(job_id,plan_id,due_at,latest_start_at,deadline_at,spec_json,spec_digest,state,assignment_epoch) VALUES('job-a',?,?,?,?,?,?,'RUNNING',1)",
                planId,
                java.sql.Timestamp.valueOf(now.minusSeconds(5)),
                java.sql.Timestamp.valueOf(now.plusMinutes(1)),
                java.sql.Timestamp.valueOf(now.plusHours(1)),
                spec,
                "0".repeat(64));
        jdbc.update(
                "INSERT INTO inspection_run(run_id,job_id,assignment_epoch,claim_request_id,control_id,control_seq,node_id,boot_id,spec_json,budget_keys_json,state,claimed_at,lease_expires_at,resources_released) VALUES('run-a','job-a',1,'claim-a','control-a',1,'test-node','boot-a',?,'[]','RUNNING',?,?,0)",
                spec,
                java.sql.Timestamp.valueOf(now.minusSeconds(5)),
                java.sql.Timestamp.valueOf(now.plusMinutes(5)));
        jdbc.update(
                "INSERT INTO storage_instance(id,name,endpoint,region,network_policy_key,encryption_key_id,secret_nonce,secret_ciphertext,secret_tag,write_enabled,version,created_by,created_at,updated_at) VALUES(7001,'evidence-test','https://127.0.0.1:9443','us-east-1','storage','test',?,?,?,1,0,?,?,?)",
                new byte[12],
                new byte[1],
                new byte[16],
                owner,
                java.sql.Timestamp.valueOf(now),
                java.sql.Timestamp.valueOf(now));
        jdbc.update(
                "INSERT INTO storage_scope(id,instance_id,bucket,prefix,probe_prefix,identity_digest,management_mode,version) VALUES(7101,7001,'evidence-bucket','evidence/','evidence/.probe/',?,'MANAGED',0)",
                new byte[32]);
        jdbc.update("UPDATE storage_default SET scope_id=7101 WHERE id=1");
        when(s3.presignedPut(
                        any(),
                        anyString(),
                        anyString(),
                        anyLong(),
                        anyString(),
                        anyString(),
                        anyInt(),
                        any()))
                .thenReturn(new StorageS3Client.PutGrant("https://127.0.0.1:9443/test", Map.of()));
    }

    @AfterEach
    void cleanupInspectionTables() {
        for (String name :
                new String[] {
                    "inspection_evidence",
                    "inspection_claim_receipt",
                    "inspection_result",
                    "inspection_run",
                    "inspection_job",
                    "inspection_budget",
                    "inspection_control"
                }) {
            jdbc.execute("DROP TABLE IF EXISTS " + name);
        }
    }

    @Test
    void machineWriteRouteRequiresBearerAndBrowserReadKeepsCameraBoundary() throws Exception {
        mvc.perform(
                        post("/inspection-nodes/evidence/authorize")
                                .contentType("application/json")
                                .content("{}"))
                .andExpect(
                        org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                                .isUnauthorized());
        mvc.perform(get("/inspection-nodes/evidence/missing"))
                .andExpect(
                        org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                                .isUnauthorized());
        mvc.perform(get("/inspection-nodes/evidence/missing").session(auth.session()))
                .andExpect(
                        org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                                .isNotFound());
        service.authorize("test-node", authorize("evidence-private", "request-private"));
        jdbc.update(
                "UPDATE inspection_evidence SET state='COMMITTED',reason_code='CONFIRMED',committed_at=CURRENT_TIMESTAMP,etag='\"etag\"' WHERE evidence_id='evidence-private'");
        mvc.perform(get("/inspection-nodes/evidence/evidence-private").session(auth.session()))
                .andExpect(
                        org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                                .isOk());
        jdbc.update("UPDATE sys_role SET code='CAMERA_OPERATOR' WHERE code='SUPER_ADMIN'");
        mvc.perform(get("/inspection-nodes/evidence/evidence-private").session(auth.session()))
                .andExpect(
                        org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                                .isNotFound());
    }

    @Test
    void concurrentAuthorizeReplaysOnePlacementAndRejectsDifferentRequest() throws Exception {
        var start = new CountDownLatch(1);
        try (var pool = Executors.newFixedThreadPool(2)) {
            var first =
                    pool.submit(
                            () -> {
                                await(start);
                                return service.authorize(
                                        "test-node", authorize("evidence-replay", "request-one"));
                            });
            var second =
                    pool.submit(
                            () -> {
                                await(start);
                                return service.authorize(
                                        "test-node", authorize("evidence-replay", "request-one"));
                            });
            start.countDown();
            var a = first.get(5, TimeUnit.SECONDS);
            var b = second.get(5, TimeUnit.SECONDS);
            assertThat(a.objectKey()).isEqualTo(b.objectKey());
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT COUNT(*) FROM inspection_evidence", Integer.class))
                    .isOne();
            assertThat(
                            catchThrowable(
                                    () ->
                                            service.authorize(
                                                    "test-node",
                                                    authorize("evidence-replay", "request-two"))))
                    .isInstanceOf(BusinessException.class);
        }
    }

    @Test
    void stopCommittedBeforeAuthorizeLockPreventsNewReservation() throws Exception {
        var locked = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        try (var pool = Executors.newFixedThreadPool(2)) {
            var stop =
                    pool.submit(
                            () ->
                                    new TransactionTemplate(transactions)
                                            .execute(
                                                    ignored -> {
                                                        jdbc.queryForObject(
                                                                "SELECT control_id FROM inspection_control WHERE control_id='control-a' FOR UPDATE",
                                                                String.class);
                                                        jdbc.update(
                                                                "UPDATE inspection_control SET desired_state='STOPPED',control_seq=2 WHERE control_id='control-a'");
                                                        locked.countDown();
                                                        await(release);
                                                        return null;
                                                    }));
            assertThat(locked.await(3, TimeUnit.SECONDS)).isTrue();
            var authorize =
                    pool.submit(
                            () ->
                                    catchThrowable(
                                            () ->
                                                    service.authorize(
                                                            "test-node",
                                                            authorize(
                                                                    "evidence-stop",
                                                                    "request-stop"))));
            Thread.sleep(100);
            release.countDown();
            stop.get(5, TimeUnit.SECONDS);
            assertThat(authorize.get(5, TimeUnit.SECONDS)).isInstanceOf(BusinessException.class);
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT COUNT(*) FROM inspection_evidence", Integer.class))
                    .isZero();
        } finally {
            release.countDown();
        }
    }

    @Test
    void planStopCommittedBeforeAuthorizationPreventsNewReservation() throws Exception {
        var locked = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        try (var pool = Executors.newFixedThreadPool(2)) {
            var stop =
                    pool.submit(
                            () ->
                                    new TransactionTemplate(transactions)
                                            .execute(
                                                    ignored -> {
                                                        jdbc.queryForObject(
                                                                "SELECT id FROM sys_role WHERE code='SUPER_ADMIN' FOR UPDATE",
                                                                Long.class);
                                                        jdbc.update(
                                                                "UPDATE camera_status_plan SET state='STOPPED' WHERE id=?",
                                                                Long.valueOf(planId));
                                                        locked.countDown();
                                                        await(release);
                                                        return null;
                                                    }));
            assertThat(locked.await(3, TimeUnit.SECONDS)).isTrue();
            var authorize =
                    pool.submit(
                            () ->
                                    catchThrowable(
                                            () ->
                                                    service.authorize(
                                                            "test-node",
                                                            authorize(
                                                                    "evidence-plan-stop",
                                                                    "request-plan-stop"))));
            Thread.sleep(100);
            assertThat(authorize.isDone()).isFalse();
            release.countDown();
            stop.get(5, TimeUnit.SECONDS);
            assertThat(authorize.get(5, TimeUnit.SECONDS)).isInstanceOf(BusinessException.class);
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT COUNT(*) FROM inspection_evidence", Integer.class))
                    .isZero();
        } finally {
            release.countDown();
        }
    }

    @Test
    void cameraAndProfileChangesCommitBeforeAuthorizationReadsThem() throws Exception {
        var locked = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        try (var pool = Executors.newFixedThreadPool(2)) {
            var revoke =
                    pool.submit(
                            () ->
                                    new TransactionTemplate(transactions)
                                            .execute(
                                                    ignored -> {
                                                        jdbc.queryForObject(
                                                                "SELECT id FROM camera_channel WHERE id=? FOR UPDATE",
                                                                Long.class,
                                                                Long.valueOf(cameraId));
                                                        jdbc.queryForObject(
                                                                "SELECT id FROM camera_stream_profile WHERE id=? FOR UPDATE",
                                                                Long.class,
                                                                Long.valueOf(profileId));
                                                        jdbc.update(
                                                                "UPDATE camera_channel SET lifecycle='DISABLED' WHERE id=?",
                                                                Long.valueOf(cameraId));
                                                        jdbc.update(
                                                                "UPDATE camera_stream_profile SET enabled=0 WHERE id=?",
                                                                Long.valueOf(profileId));
                                                        locked.countDown();
                                                        await(release);
                                                        return null;
                                                    }));
            assertThat(locked.await(3, TimeUnit.SECONDS)).isTrue();
            var authorize =
                    pool.submit(
                            () ->
                                    catchThrowable(
                                            () ->
                                                    service.authorize(
                                                            "test-node",
                                                            authorize(
                                                                    "evidence-camera-revoke",
                                                                    "request-camera-revoke"))));
            Thread.sleep(100);
            assertThat(authorize.isDone()).isFalse();
            release.countDown();
            revoke.get(5, TimeUnit.SECONDS);
            assertThat(authorize.get(5, TimeUnit.SECONDS)).isInstanceOf(BusinessException.class);
            assertThat(
                            jdbc.queryForObject(
                                    "SELECT COUNT(*) FROM inspection_evidence", Integer.class))
                    .isZero();
        } finally {
            release.countDown();
        }
    }

    @Test
    void slowRemoteVerificationDoesNotHoldDatabaseConnection() throws Exception {
        service.authorize("test-node", authorize("evidence-slow", "request-slow"));
        var headEntered = new CountDownLatch(1);
        var headRelease = new CountDownLatch(1);
        var getEntered = new CountDownLatch(1);
        var getRelease = new CountDownLatch(1);
        var object =
                new StorageS3Client.ObjectInfo(
                        "evidence/inspections/run-a/evidence-slow/1.png",
                        3,
                        "\"etag\"",
                        Instant.now(),
                        "image/png",
                        null);
        when(s3.head(any(), anyString(), anyString()))
                .thenAnswer(
                        ignored -> {
                            headEntered.countDown();
                            await(headRelease);
                            return object;
                        });
        when(s3.content(any(), anyString(), any(), isNull()))
                .thenAnswer(ignored -> response(getEntered, getRelease));
        var hikari = dataSource.unwrap(HikariDataSource.class).getHikariPoolMXBean();
        try (var pool = Executors.newSingleThreadExecutor()) {
            var future =
                    pool.submit(() -> service.complete("test-node", complete("evidence-slow")));
            assertThat(headEntered.await(3, TimeUnit.SECONDS)).isTrue();
            assertOnlineResponsive("HEAD", hikari);
            headRelease.countDown();
            assertThat(getEntered.await(3, TimeUnit.SECONDS)).isTrue();
            assertOnlineResponsive("GET", hikari);
            getRelease.countDown();
            assertThat(future.get(5, TimeUnit.SECONDS).state()).isEqualTo("COMMITTED");
        } finally {
            headRelease.countDown();
            getRelease.countDown();
        }
    }

    private void assertOnlineResponsive(String phase, com.zaxxer.hikari.HikariPoolMXBean hikari)
            throws Exception {
        assertThat(hikari.getActiveConnections()).isZero();
        int maxActive = 0;
        int maxWaiting = 0;
        long start = System.nanoTime();
        for (int i = 0; i < 30; i++) {
            assertThat(
                            mvc.perform(
                                            get("/camera-status?layer=CONTROL&size=20")
                                                    .session(auth.session()))
                                    .andReturn()
                                    .getResponse()
                                    .getStatus())
                    .isEqualTo(200);
            maxActive = Math.max(maxActive, hikari.getActiveConnections());
            maxWaiting = Math.max(maxWaiting, hikari.getThreadsAwaitingConnection());
        }
        assertThat(maxWaiting).isZero();
        System.out.printf(
                "Inspection remote %s: onlineRequests=30 errors=0 maxActive=%d maxWaiting=%d elapsedMs=%d%n",
                phase,
                maxActive,
                maxWaiting,
                TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - start));
    }

    private InspectionEvidenceCommands.Authorize authorize(String id, String request)
            throws Exception {
        String checksum =
                Base64.getEncoder()
                        .encodeToString(
                                MessageDigest.getInstance("SHA-256")
                                        .digest("abc".getBytes(StandardCharsets.UTF_8)));
        return new InspectionEvidenceCommands.Authorize(
                request, id, "run-a", 1, "trace-fixture", "IMAGE", 3L, "image/png", checksum);
    }

    private static InspectionEvidenceCommands.Complete complete(String id) {
        return new InspectionEvidenceCommands.Complete(
                "complete-fixture",
                id,
                "run-a",
                1,
                1,
                "trace-fixture",
                "PUT_ACCEPTED",
                200,
                3L,
                "\"etag\"",
                null,
                "UPLOAD_AWAITING_CONFIRMATION",
                null,
                0,
                null,
                "UPLOAD_RESPONSE",
                "",
                true);
    }

    private static StorageHttpTransport.Response response(
            CountDownLatch entered, CountDownLatch release) {
        InputStream body =
                new ByteArrayInputStream("abc".getBytes(StandardCharsets.UTF_8)) {
                    private boolean started;

                    @Override
                    public synchronized int read(byte[] bytes, int offset, int length) {
                        if (!started) {
                            started = true;
                            entered.countDown();
                            await(release);
                        }
                        return super.read(bytes, offset, length);
                    }
                };
        return new StorageHttpTransport.Response(
                200,
                Map.of(),
                body,
                new Socket(),
                new Socket(),
                org.mockito.Mockito.mock(ScheduledFuture.class),
                new Semaphore(1),
                new AtomicBoolean());
    }

    private static void await(CountDownLatch latch) {
        try {
            if (!latch.await(5, TimeUnit.SECONDS))
                throw new AssertionError("fixture latch timeout");
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
            throw new AssertionError(error);
        }
    }

    private void createInspectionTables() throws Exception {
        try (var connection = dataSource.getConnection()) {
            for (String name :
                    new String[] {
                        "inspection_evidence",
                        "inspection_claim_receipt",
                        "inspection_result",
                        "inspection_run",
                        "inspection_job",
                        "inspection_budget",
                        "inspection_control"
                    }) jdbc.execute("DROP TABLE IF EXISTS " + name);
            for (int i = 1; i <= 7; i++) {
                String suffix =
                        switch (i) {
                            case 1 -> "control";
                            case 2 -> "budget";
                            case 3 -> "job";
                            case 4 -> "run";
                            case 5 -> "result";
                            case 6 -> "claim-receipt";
                            default -> "evidence";
                        };
                String sql =
                        Files.readString(
                                        Path.of(
                                                "sql",
                                                "业务",
                                                "巡检P0",
                                                "%02d-%s.sql".formatted(i, suffix)))
                                .replace("CHARACTER SET ascii COLLATE ascii_bin", "")
                                .replace("ENGINE=InnoDB DEFAULT CHARSET=utf8mb4", "")
                                .replace("JSON", "CLOB");
                ScriptUtils.executeSqlScript(
                        connection,
                        new EncodedResource(
                                new ByteArrayResource(sql.getBytes(StandardCharsets.UTF_8)),
                                StandardCharsets.UTF_8));
            }
        }
    }
}
