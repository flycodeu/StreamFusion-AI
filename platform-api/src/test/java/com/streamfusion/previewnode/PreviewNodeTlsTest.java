package com.streamfusion.previewnode;

import static org.assertj.core.api.Assertions.*;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.control.PreviewControl.*;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor.SourceKey;
import com.streamfusion.platform.media.preview.node.PreviewNodeClient;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class PreviewNodeTlsTest {
    @TempDir Path directory;

    PreviewDeadline budget() {
        return PreviewDeadline.after(Duration.ofSeconds(5));
    }

    @Test
    void servesOnlyAuthenticatedPinnedPreviewControlAndRejectsStaleIdentity() throws Exception {
        try (var fixture = new PreviewControlTestFixture(directory);
                var client = fixture.client()) {
            var snapshot = client.get("snapshot", Snapshot.class, budget());
            assertThat(snapshot.state()).isEqualTo("READY");
            var command =
                    new Command(
                            1,
                            "test-a",
                            snapshot.bootId(),
                            UUID.randomUUID().toString(),
                            "trace",
                            UUID.randomUUID().toString(),
                            1,
                            snapshot.configVersion(),
                            System.currentTimeMillis() + 4000,
                            Action.CREATE,
                            new SourceKey(1L, "connection"),
                            "rtsp://user:pw@192.0.2.10:554/live",
                            List.of("H264"),
                            null);
            assertThat(client.apply(command, budget()).state()).isEqualTo("SUCCEEDED");
            assertThat(
                            client.get(
                                            "operations/" + command.operationId(),
                                            Receipt.class,
                                            budget())
                                    .state())
                    .isEqualTo("SUCCEEDED");
            assertThatThrownBy(() -> client.get("users", Snapshot.class, budget()))
                    .hasMessage("NOT_FOUND");
            var stale =
                    new Command(
                            1,
                            "wrong-node",
                            snapshot.bootId(),
                            UUID.randomUUID().toString(),
                            "trace",
                            command.resourceId(),
                            1,
                            snapshot.configVersion(),
                            System.currentTimeMillis() + 4000,
                            Action.STOP,
                            null,
                            null,
                            null,
                            null);
            assertThatThrownBy(() -> client.apply(stale, budget()))
                    .hasMessage("NODE_IDENTITY_CHANGED");
            Path wrongToken = directory.resolve("old-token");
            Files.writeString(wrongToken, "x".repeat(48));
            try (var unauthorized =
                    new PreviewNodeClient(fixture.origin, fixture.pin, wrongToken, fixture.json)) {
                assertThatThrownBy(() -> unauthorized.get("snapshot", Snapshot.class, budget()))
                        .hasMessage("NODE_UNAUTHORIZED");
                Files.writeString(fixture.token, "x".repeat(48));
                assertThat(unauthorized.get("snapshot", Snapshot.class, budget()).nodeId())
                        .isEqualTo("test-a");
                Path revokedToken = directory.resolve("revoked-token");
                Files.writeString(revokedToken, "a".repeat(48));
                try (var revoked =
                        new PreviewNodeClient(
                                fixture.origin, fixture.pin, revokedToken, fixture.json)) {
                    assertThatThrownBy(() -> revoked.get("snapshot", Snapshot.class, budget()))
                            .hasMessage("NODE_UNAUTHORIZED");
                }
            }
        }
    }

    @Test
    void rejectsAnUnpinnedServerCertificate() throws Exception {
        try (var fixture = new PreviewControlTestFixture(directory);
                var client =
                        new PreviewNodeClient(
                                fixture.origin, "0".repeat(64), fixture.token, fixture.json)) {
            assertThatThrownBy(() -> client.get("snapshot", Snapshot.class, budget()))
                    .hasMessage("NODE_UNREACHABLE");
        }
    }

    @Test
    void maintenanceUsesPinnedAuthenticatedControlAndLeavesNodeSealedAfterOpsFinishes()
            throws Exception {
        try (var fixture = new PreviewControlTestFixture(directory);
                var client = fixture.client()) {
            var state = client.get("snapshot", Snapshot.class, budget());
            String permit = UUID.randomUUID().toString(), job = UUID.randomUUID().toString();
            for (String action : List.of("PREPARE", "CLAIM", "FINISH")) {
                var command =
                        new com.streamfusion.platform.media.preview.control.PreviewMaintenance
                                .Command(
                                1,
                                "test-a",
                                state.bootId(),
                                state.configVersion(),
                                action,
                                permit,
                                job,
                                System.currentTimeMillis() + 4000);
                var result = client.maintain(command, budget());
                assertThat(result.permitId()).isEqualTo(permit);
            }
            assertThat(client.get("snapshot", Snapshot.class, budget()).state())
                    .isEqualTo("MAINTENANCE");
            assertThat(
                            client.get(
                                            "maintenance",
                                            com.streamfusion.platform.media.preview.control
                                                    .PreviewMaintenance.State.class,
                                            budget())
                                    .state())
                    .isEqualTo("COMPLETE");
            Path wrong = directory.resolve("invalid-token");
            Files.writeString(wrong, "x".repeat(48));
            try (var unauthorized =
                    new PreviewNodeClient(fixture.origin, fixture.pin, wrong, fixture.json)) {
                assertThatThrownBy(
                                () ->
                                        unauthorized.get(
                                                "maintenance",
                                                com.streamfusion.platform.media.preview.control
                                                        .PreviewMaintenance.State.class,
                                                budget()))
                        .hasMessage("NODE_UNAUTHORIZED");
            }
        }
    }
}
