package com.streamfusion.previewnode;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewControl.*;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneId;
import java.util.List;
import java.util.UUID;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;

class PreviewNodeServiceTest {
    @TempDir Path directory;
    final ObjectMapper json = new ObjectMapper().findAndRegisterModules();
    final PreviewExecutor core = mock(PreviewExecutor.class);
    final MutableClock clock = new MutableClock();
    PreviewNodeJournal journal;
    PreviewNodeService node;

    @BeforeEach
    void open() {
        journal = new PreviewNodeJournal(directory, json);
        node = service(64, () -> {});
        when(core.start(anyString(), anyList(), any(), any()))
                .thenReturn(new PreviewExecutor.Playback("H264", false));
        when(core.missingSourceViewers(anyString(), any())).thenReturn(List.of());
        node.tick();
    }

    @AfterEach
    void close() {
        journal.close();
    }

    PreviewNodeService service(int slots, Runnable health) {
        return new PreviewNodeService(
                "node-a",
                "c".repeat(64),
                slots,
                64,
                core,
                journal,
                new PreviewNodeSourcePolicy(List.of("192.0.2.10:554")),
                json,
                clock,
                health);
    }

    Command command(String viewer, Action action) {
        var state = node.snapshot();
        return new Command(
                1,
                "node-a",
                state.bootId(),
                UUID.randomUUID().toString(),
                "trace_test",
                viewer,
                1,
                state.configVersion(),
                clock.millis() + 18000,
                action,
                new PreviewExecutor.SourceKey(1L, "connection-v1"),
                "rtsp://camera-user:secret-password@192.0.2.10:554/live",
                List.of("H264"),
                "v=0\r\n");
    }

    @Test
    void deduplicatesCreateAndRejectsChangedPayloadWithoutClosingExistingViewer() throws Exception {
        String id = UUID.randomUUID().toString();
        var create = command(id, Action.CREATE);
        assertThat(node.execute(create).state()).isEqualTo("SUCCEEDED");
        assertThat(node.execute(create)).isEqualTo(node.query(create.operationId()));
        verify(core, times(1)).prepare(id);
        var conflict =
                new Command(
                        1,
                        create.nodeId(),
                        create.bootId(),
                        create.operationId(),
                        create.traceId(),
                        id,
                        1,
                        create.configVersion(),
                        create.deadline(),
                        Action.STOP,
                        null,
                        null,
                        null,
                        null);
        assertThatThrownBy(() -> node.execute(conflict)).hasMessage("OPERATION_CONFLICT");
        assertThat(node.execute(command(id, Action.CREATE)).reasonCode())
                .isEqualTo("RESOURCE_CONFLICT");
        assertThat(node.resource(id).state()).isEqualTo("READY");
        String persisted = Files.readString(directory.resolve("preview-node-control.json"));
        assertThat(persisted).doesNotContain("secret-password", "rtsp://", "camera-user", "v=0");
    }

    @Test
    void renewsOnlyLiveViewerAndExpiryRetainsFailedCleanupUntilConfirmed() {
        String id = UUID.randomUUID().toString();
        node.execute(command(id, Action.CREATE));
        node.execute(command(id, Action.RENEW));
        clock.advance(89);
        node.tick();
        assertThat(node.resource(id).state()).isEqualTo("READY");
        clock.advance(2);
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(core).close(eq(id), any());
        node.tick();
        assertThat(node.resource(id).state()).isEqualTo("CLOSING");
        assertThat(node.execute(command(id, Action.RENEW)).reasonCode()).isEqualTo("LEASE_EXPIRED");
        doNothing().when(core).close(eq(id), any());
        assertThat(node.execute(command(id, Action.STOP)).state()).isEqualTo("SUCCEEDED");
        assertThat(node.resource(id).state()).isEqualTo("CLOSED");
    }

    @Test
    void stopBeforeCreateIsADurableTombstone() {
        String id = UUID.randomUUID().toString();
        node.execute(command(id, Action.STOP));
        assertThat(node.execute(command(id, Action.CREATE)).reasonCode())
                .isEqualTo("RESOURCE_CONFLICT");
        verify(core, never()).prepare(anyString());
    }

    @Test
    void budgetAndSourceGenerationGateIncludePendingCleanup() {
        node = service(1, () -> {});
        node.tick();
        String first = UUID.randomUUID().toString();
        node.execute(command(first, Action.CREATE));
        assertThat(node.execute(command(UUID.randomUUID().toString(), Action.CREATE)).reasonCode())
                .isEqualTo("PREVIEW_CAPACITY");
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(core).close(eq(first), any());
        node.execute(command(first, Action.STOP));
        assertThat(node.execute(command(UUID.randomUUID().toString(), Action.CREATE)).reasonCode())
                .isEqualTo("PREVIEW_CAPACITY");
    }

    @Test
    void aHundredConcurrentRequestsCannotOverbookTheLastViewerSlot() throws Exception {
        node = service(1, () -> {});
        node.tick();
        try (var workers = java.util.concurrent.Executors.newFixedThreadPool(16)) {
            var jobs = new java.util.ArrayList<java.util.concurrent.Future<Receipt>>();
            for (int i = 0; i < 100; i++) {
                var request = command(UUID.randomUUID().toString(), Action.CREATE);
                jobs.add(workers.submit(() -> node.execute(request)));
            }
            int succeeded = 0;
            for (var job : jobs)
                if (job.get(20, java.util.concurrent.TimeUnit.SECONDS).state().equals("SUCCEEDED"))
                    succeeded++;
            assertThat(succeeded).isOne();
            assertThat(node.snapshot().resources()).hasSize(1);
            verify(core, times(1)).prepare(anyString());
        }
    }

    @Test
    void aNewSourceGenerationWaitsForTheOldGenerationToClose() {
        String first = UUID.randomUUID().toString();
        node.execute(command(first, Action.CREATE));
        var old = command(UUID.randomUUID().toString(), Action.CREATE);
        var changed =
                new Command(
                        old.protocolVersion(),
                        old.nodeId(),
                        old.bootId(),
                        old.operationId(),
                        old.traceId(),
                        old.resourceId(),
                        old.generation(),
                        old.configVersion(),
                        old.deadline(),
                        old.action(),
                        new PreviewExecutor.SourceKey(1L, "connection-v2"),
                        old.sourceUrl(),
                        old.videoCodecs(),
                        old.sdp());
        assertThat(node.execute(changed).reasonCode()).isEqualTo("SOURCE_GENERATION_PENDING");
        assertThat(node.resource(first).state()).isEqualTo("READY");
    }

    @Test
    void restartDoesNotReleaseOldOwnershipUntilCoreRecoveryConfirmsExit() {
        String id = UUID.randomUUID().toString();
        var old = command(id, Action.CREATE);
        node.execute(old);
        journal.close();
        journal = new PreviewNodeJournal(directory, json);
        node = service(64, () -> {});
        assertThat(node.snapshot().state()).isEqualTo("RECONCILING");
        assertThatThrownBy(() -> node.execute(old)).hasMessage("NODE_IDENTITY_CHANGED");
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(core).requireRecovered();
        assertThatThrownBy(node::tick).hasMessage("CLEANUP_PENDING");
        assertThat(node.resource(id).state()).isEqualTo("READY");
        doNothing().when(core).requireRecovered();
        node.tick();
        assertThat(node.resource(id).state()).isEqualTo("CLOSED");
        assertThat(node.query(old.operationId()).state()).isEqualTo("SUCCEEDED");
    }

    @Test
    void lateSuccessIsNotReturnedAsReadyAndIsReaped() {
        String id = UUID.randomUUID().toString();
        when(core.start(anyString(), anyList(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            clock.advance(20);
                            throw new PreviewFailure("PREVIEW_TIMEOUT");
                        });
        assertThat(node.execute(command(id, Action.CREATE)).state()).isEqualTo("FAILED");
        assertThat(node.resource(id).state()).isEqualTo("CLOSING");
        node.tick();
        assertThat(node.resource(id).state()).isEqualTo("CLOSED");
    }

    @Test
    void runtimeFailureBlocksNewPullsButAllowsExpiredOwnershipCleanup() {
        String id = UUID.randomUUID().toString();
        node.execute(command(id, Action.CREATE));
        node =
                service(
                        64,
                        () -> {
                            throw new PreviewFailure("NODE_MEDIA_CONFIGURATION_INVALID");
                        });
        node.tick();
        assertThat(node.snapshot().state()).isEqualTo("DEGRADED");
        assertThat(node.execute(command(UUID.randomUUID().toString(), Action.CREATE)).reasonCode())
                .isEqualTo("NODE_MEDIA_UNAVAILABLE");
        assertThat(node.resource(id).state()).isEqualTo("CLOSED");
    }

    @Test
    void journalRejectsASecondOwnerAndCorruption() throws Exception {
        assertThatThrownBy(() -> new PreviewNodeJournal(directory, json))
                .hasMessage("NODE_JOURNAL_UNAVAILABLE");
        Path corrupt = directory.resolve("corrupt");
        Files.createDirectories(corrupt);
        Files.writeString(corrupt.resolve("preview-node-control.json"), "{\"format\":999}");
        assertThatThrownBy(() -> new PreviewNodeJournal(corrupt, json))
                .hasMessage("NODE_JOURNAL_UNAVAILABLE");
    }

    @Test
    void mediaAllowlistRejectsNamesAlternatePortsAndAmbiguousNumericHosts() {
        var policy = new PreviewNodeSourcePolicy(List.of("192.0.2.10:554", "[2001:db8::1]:8554"));
        assertThat(policy.require("rtsp://user:pw@192.0.2.10:554/live").getHost())
                .isEqualTo("192.0.2.10");
        assertThat(policy.require("rtsp://[2001:db8::1]:8554/live")).isNotNull();
        for (String rejected :
                List.of(
                        "rtsp://camera.example/live",
                        "rtsp://192.0.2.10:80/live",
                        "http://192.0.2.10:554/live",
                        "rtsp://0300.0.2.10:554/live",
                        "rtsp://127.1/live",
                        "rtsp://192.0.2.10/live#fragment"))
            assertThatThrownBy(() -> policy.require(rejected)).hasMessage("DESTINATION_REJECTED");
    }

    @Test
    void concurrentOperationIdCannotOwnTwoDifferentResources() throws Exception {
        var original = command(UUID.randomUUID().toString(), Action.CREATE);
        var other =
                new Command(
                        1,
                        original.nodeId(),
                        original.bootId(),
                        original.operationId(),
                        original.traceId(),
                        UUID.randomUUID().toString(),
                        1,
                        original.configVersion(),
                        original.deadline(),
                        Action.CREATE,
                        original.source(),
                        original.sourceUrl(),
                        original.videoCodecs(),
                        original.sdp());
        try (var pool = java.util.concurrent.Executors.newFixedThreadPool(2)) {
            var gate = new java.util.concurrent.CountDownLatch(1);
            var results = new java.util.ArrayList<java.util.concurrent.Future<String>>();
            for (var request : List.of(original, other))
                results.add(
                        pool.submit(
                                () -> {
                                    gate.await();
                                    try {
                                        return node.execute(request).state();
                                    } catch (PreviewFailure failure) {
                                        return failure.reason();
                                    }
                                }));
            gate.countDown();
            assertThat(
                            List.of(
                                    results.get(0).get(5, java.util.concurrent.TimeUnit.SECONDS),
                                    results.get(1).get(5, java.util.concurrent.TimeUnit.SECONDS)))
                    .containsExactlyInAnyOrder("SUCCEEDED", "OPERATION_CONFLICT");
        }
        verify(core, times(1)).prepare(anyString());
        assertThat(node.snapshot().resources()).hasSize(1);
    }

    com.streamfusion.platform.media.preview.control.PreviewMaintenance.Command maintenance(
            String action, String permit, String job) {
        var observed = node.snapshot();
        return new com.streamfusion.platform.media.preview.control.PreviewMaintenance.Command(
                1,
                "node-a",
                observed.bootId(),
                observed.configVersion(),
                action,
                permit,
                job,
                clock.millis() + 10000);
    }

    @Test
    void maintenanceCannotInterruptExistingViewerAndRequiresConfirmedCleanup() {
        String viewer = UUID.randomUUID().toString(), permit = UUID.randomUUID().toString();
        node.execute(command(viewer, Action.CREATE));
        assertThatThrownBy(() -> node.maintain(maintenance("PREPARE", permit, null)))
                .hasMessage("NODE_DRAIN_REQUIRED");
        assertThat(node.execute(command(viewer, Action.RENEW)).state()).isEqualTo("SUCCEEDED");
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(core).close(eq(viewer), any());
        node.execute(command(viewer, Action.STOP));
        assertThatThrownBy(() -> node.maintain(maintenance("PREPARE", permit, null)))
                .hasMessage("NODE_DRAIN_REQUIRED");
        assertThat(node.maintenanceState().state()).isEqualTo("OPEN");
    }

    @Test
    void maintenanceSealSurvivesRestartAndDoesNotExpireIntoAdmission() {
        String permit = UUID.randomUUID().toString();
        node.maintain(maintenance("PREPARE", permit, null));
        assertThat(node.execute(command(UUID.randomUUID().toString(), Action.CREATE)).reasonCode())
                .isEqualTo("NODE_MAINTENANCE");
        clock.advance(601);
        node = service(64, () -> {});
        node.tick();
        assertThat(node.snapshot().state()).isEqualTo("MAINTENANCE");
        assertThatThrownBy(
                        () ->
                                node.maintain(
                                        maintenance("CLAIM", permit, UUID.randomUUID().toString())))
                .hasMessage("MAINTENANCE_PERMIT_STALE");
        node.maintain(maintenance("RELEASE", permit, null));
        assertThat(node.snapshot().state()).isEqualTo("READY");
        assertThat(node.execute(command(UUID.randomUUID().toString(), Action.CREATE)).state())
                .isEqualTo("SUCCEEDED");
    }

    @Test
    void opsClaimIsIdempotentExclusiveAndRequiresExplicitReleaseAfterRestart() {
        String permit = UUID.randomUUID().toString(), job = UUID.randomUUID().toString();
        var prepare = maintenance("PREPARE", permit, null);
        assertThat(node.maintain(prepare)).isEqualTo(node.maintain(prepare));
        var claimed = node.maintain(maintenance("CLAIM", permit, job));
        assertThat(node.maintain(maintenance("CLAIM", permit, job))).isEqualTo(claimed);
        assertThatThrownBy(() -> node.maintain(maintenance("RELEASE", permit, null)))
                .hasMessage("MAINTENANCE_IN_PROGRESS");
        assertThatThrownBy(
                        () ->
                                node.maintain(
                                        maintenance("CLAIM", permit, UUID.randomUUID().toString())))
                .hasMessage("MAINTENANCE_PERMIT_STALE");
        node = service(64, () -> {});
        node.tick();
        assertThat(node.maintenanceState().state()).isEqualTo("CLAIMED");
        assertThatThrownBy(
                        () ->
                                node.maintain(
                                        maintenance(
                                                "FINISH", permit, UUID.randomUUID().toString())))
                .hasMessage("MAINTENANCE_JOB_MISMATCH");
        node.maintain(maintenance("FINISH", permit, job));
        assertThat(node.snapshot().state()).isEqualTo("MAINTENANCE");
        assertThatThrownBy(() -> node.maintain(maintenance("CLAIM", permit, job)))
                .hasMessage("MAINTENANCE_PERMIT_STALE");
        node.maintain(maintenance("RELEASE", permit, null));
        assertThat(node.snapshot().maxSessions()).isEqualTo(64);
        assertThat(node.snapshot().maxTranscodes()).isEqualTo(64);
    }

    @Test
    void maintenanceAndCreateCannotBothWinAdmissionRace() throws Exception {
        for (int i = 0; i < 20; i++) {
            String permit = UUID.randomUUID().toString(), viewer = UUID.randomUUID().toString();
            var gate = new java.util.concurrent.CyclicBarrier(2);
            try (var workers = java.util.concurrent.Executors.newFixedThreadPool(2)) {
                var create =
                        workers.submit(
                                () -> {
                                    gate.await();
                                    return node.execute(command(viewer, Action.CREATE));
                                });
                var seal =
                        workers.submit(
                                () -> {
                                    gate.await();
                                    try {
                                        node.maintain(maintenance("PREPARE", permit, null));
                                        return true;
                                    } catch (PreviewFailure rejected) {
                                        assertThat(rejected.reason())
                                                .isEqualTo("NODE_DRAIN_REQUIRED");
                                        return false;
                                    }
                                });
                var created = create.get(5, java.util.concurrent.TimeUnit.SECONDS);
                boolean sealed = seal.get(5, java.util.concurrent.TimeUnit.SECONDS);
                assertThat(sealed && created.state().equals("SUCCEEDED")).isFalse();
                node.execute(command(viewer, Action.STOP));
                if (sealed) node.maintain(maintenance("RELEASE", permit, null));
            }
        }
    }

    @Test
    void damagedMaintenanceFileFailsClosed() throws Exception {
        Files.writeString(directory.resolve("preview-maintenance.json"), "{broken");
        assertThatThrownBy(() -> service(64, () -> {})).hasMessage("NODE_MAINTENANCE_DAMAGED");
    }

    static class MutableClock extends Clock {
        long time = System.currentTimeMillis();

        void advance(int seconds) {
            time += seconds * 1000L;
        }

        public ZoneId getZone() {
            return ZoneId.of("UTC");
        }

        public Clock withZone(ZoneId zone) {
            return this;
        }

        public Instant instant() {
            return Instant.ofEpochMilli(time);
        }

        public long millis() {
            return time;
        }
    }
}
