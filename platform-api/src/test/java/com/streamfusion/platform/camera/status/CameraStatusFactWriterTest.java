package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.mockito.ArgumentCaptor;

class CameraStatusFactWriterTest {
    private static final Instant NOW = Instant.parse("2026-10-17T02:00:00Z");
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private static final byte[] DIGEST = {1, 2, 3};
    private final CameraStatusSnapshotMapper snapshots = mock(CameraStatusSnapshotMapper.class);
    private final CameraRtspTargetService targets = mock(CameraRtspTargetService.class);
    private final CameraStatusFactWriter writer =
            new CameraStatusFactWriter(
                    snapshots,
                    targets,
                    new CameraStatusProperties(),
                    Clock.fixed(NOW, ZoneOffset.UTC));

    @Test
    void requestTimeAndIdOrderSnapshotsWhileOlderReceiptsStillKeepTheirObservation() {
        var previous = previous();
        var check = check();
        when(snapshots.get(3L, "CONTROL")).thenReturn(previous);
        check.setRequestedAt(previous.getCheckRequestedAt().minusSeconds(1));
        writer.prepareObservation(check, failed());
        assertThat(check.getSnapshotApplied()).isFalse();
        assertThat(check.getResultValue()).isEqualTo("FAILED");
        assertThat(check.getObservedAt()).isEqualTo(local(NOW));
        assertThat(check.getReadAt()).isEqualTo(check.getObservedAt());
        assertThat(check.getValidUntil()).isEqualTo(local(NOW.plusSeconds(300)));
        check.setRequestedAt(previous.getCheckRequestedAt());
        check.setId(previous.getCheckId());
        writer.prepareObservation(check, failed());
        assertThat(check.getSnapshotApplied()).isFalse();
        check.setId(previous.getCheckId() + 1);
        writer.prepareObservation(check, failed());
        assertThat(check.getSnapshotApplied()).isTrue();
        verify(snapshots, never()).insert(any());
        verify(snapshots, never()).update(any());
    }

    @Test
    void mediaEvidenceRequiresCompleteBoundedSamplesAndMatchingOutcome() {
        var check = check();
        check.setLayer("MEDIA");
        var valid = evidence(NOW.minusSeconds(8), NOW.minusSeconds(2), 10, 15, 3, 2000);
        assertThat(writer.validMediaFact(check, media("MEDIA_RECEIVING", valid))).isTrue();
        var noFrames = evidence(NOW.minusSeconds(8), NOW.minusSeconds(2), 10, 10, 3, 2000);
        assertThat(
                        writer.validMediaFact(
                                check,
                                new CameraProbeResult(
                                        CameraProbeResult.Outcome.FAILED,
                                        "MEDIA_NO_FRAMES",
                                        0,
                                        6000,
                                        noFrames)))
                .isTrue();
        var invalid =
                List.of(
                        evidence(NOW.minusSeconds(11), NOW.minusSeconds(2), 10, 15, 3, 2000),
                        evidence(NOW.minusSeconds(8), NOW.plusSeconds(1), 10, 15, 3, 2000),
                        evidence(NOW.minusSeconds(4), NOW.minusSeconds(1), 10, 15, 3, 2000),
                        evidence(NOW.minusSeconds(8), NOW.minusSeconds(2), -1, 15, 3, 2000),
                        evidence(NOW.minusSeconds(8), NOW.minusSeconds(2), 10, 10, 3, 2000),
                        evidence(NOW.minusSeconds(8), NOW.minusSeconds(2), 10, 15, 2, 2000),
                        evidence(NOW.minusSeconds(8), NOW.minusSeconds(2), 10, 15, 3, -1));
        for (var value : invalid)
            assertThat(writer.validMediaFact(check, media("MEDIA_RECEIVING", value)))
                    .as("evidence %s", value)
                    .isFalse();
        assertThat(writer.validMediaFact(check, media("MEDIA_NO_FRAMES", valid))).isFalse();
        assertThat(writer.validMediaFact(check, media("MEDIA_RECEIVING", null))).isFalse();
    }

    @Test
    void repeatedFailureRetainsTheSameConnectionTimelineAndLastSuccess() {
        var previous = previous();
        var check = check();
        var camera = new CameraChannelEntity();
        var profile = new CameraProfileEntity();
        when(snapshots.get(3L, "CONTROL")).thenReturn(previous);
        when(targets.inspect(eq(camera), eq(profile), eq("key"), any()))
                .thenReturn(new CameraRtspTargetService.Inspection(null, DIGEST, null));
        assertThat(writer.prepareObservation(check, failed())).isSameAs(previous);
        writer.writeSnapshot(check, previous, camera, profile);
        var saved = ArgumentCaptor.forClass(CameraStatusSnapshotEntity.class);
        verify(snapshots).update(saved.capture());
        assertThat(saved.getValue().getStateSince()).isEqualTo(previous.getStateSince());
        assertThat(saved.getValue().getLastAccessibleAt())
                .isEqualTo(previous.getLastAccessibleAt());
        assertThat(saved.getValue().getLastFailedAt()).isEqualTo(local(NOW));
        assertThat(saved.getValue().getCheckId()).isEqualTo(check.getId());
        assertThat(saved.getValue().getLayer()).isEqualTo("CONTROL");
    }

    @Test
    void changedConnectionStartsANewTimelineWithoutCarryingPriorSuccess() {
        var previous = previous();
        var check = check();
        var camera = new CameraChannelEntity();
        var profile = new CameraProfileEntity();
        when(snapshots.get(3L, "CONTROL")).thenReturn(previous);
        when(targets.inspect(eq(camera), eq(profile), eq("key"), any()))
                .thenReturn(new CameraRtspTargetService.Inspection(null, new byte[] {9}, null));
        writer.prepareObservation(check, failed());
        writer.writeSnapshot(check, previous, camera, profile);
        var saved = ArgumentCaptor.forClass(CameraStatusSnapshotEntity.class);
        verify(snapshots).update(saved.capture());
        assertThat(saved.getValue().getStateSince()).isEqualTo(local(NOW));
        assertThat(saved.getValue().getLastAccessibleAt()).isNull();
        assertThat(saved.getValue().getLastFailedAt()).isEqualTo(local(NOW));
    }

    @Test
    void firstMediaSnapshotKeepsItsOwnLayerAndSampleWindow() {
        var check = check();
        check.setLayer("MEDIA");
        check.setProbeKind("ZLM_RTP_FRAMES");
        check.setReasonCode("MEDIA_RECEIVING");
        var evidence = evidence(NOW.minusSeconds(8), NOW.minusSeconds(2), 10, 15, 3, 2000);
        var previous = writer.prepareObservation(check, media("MEDIA_RECEIVING", evidence));
        assertThat(previous).isNull();
        assertThat(check.getSnapshotApplied()).isTrue();
        writer.writeSnapshot(check, previous, new CameraChannelEntity(), new CameraProfileEntity());
        var saved = ArgumentCaptor.forClass(CameraStatusSnapshotEntity.class);
        verify(snapshots).get(3L, "MEDIA");
        verify(snapshots).insert(saved.capture());
        assertThat(saved.getValue().getLayer()).isEqualTo("MEDIA");
        assertThat(saved.getValue().getMediaWindowStartedAt())
                .isEqualTo(local(evidence.windowStartedAt()));
        assertThat(saved.getValue().getMediaWindowEndedAt())
                .isEqualTo(local(evidence.windowEndedAt()));
        assertThat(saved.getValue().getMediaFirstFrames()).isEqualTo(10);
        assertThat(saved.getValue().getMediaLastFrames()).isEqualTo(15);
        assertThat(saved.getValue().getMediaSampleCount()).isEqualTo(3);
        verifyNoInteractions(targets);
    }

    private static CameraStatusCheckEntity check() {
        var check = new CameraStatusCheckEntity();
        check.setId(20L);
        check.setCameraId(2L);
        check.setProfileId(3L);
        check.setRequestedAt(local(NOW.minusSeconds(10)));
        check.setStartedAt(local(NOW.minusSeconds(10)));
        check.setLayer("CONTROL");
        check.setProbeKind("RTSP_DESCRIBE");
        check.setReasonCode("RTSP_UNREACHABLE");
        check.setConnectionKeyId("key");
        check.setConnectionDigest(DIGEST);
        return check;
    }

    private static CameraStatusSnapshotEntity previous() {
        var previous = new CameraStatusSnapshotEntity();
        previous.setCheckId(10L);
        previous.setCheckRequestedAt(local(NOW.minusSeconds(30)));
        previous.setObservedAt(local(NOW.minusSeconds(20)));
        previous.setStateSince(local(NOW.minusSeconds(60)));
        previous.setLastAccessibleAt(local(NOW.minusSeconds(90)));
        previous.setLastFailedAt(local(NOW.minusSeconds(20)));
        previous.setValue("FAILED");
        previous.setConnectionKeyId("key");
        previous.setConnectionDigest(DIGEST);
        return previous;
    }

    private static CameraProbeResult failed() {
        return new CameraProbeResult(
                CameraProbeResult.Outcome.FAILED, "RTSP_UNREACHABLE", 0, 1000, null);
    }

    private static CameraProbeResult media(String reason, CameraMediaEvidence evidence) {
        return new CameraProbeResult(
                CameraProbeResult.Outcome.ACCESSIBLE, reason, 0, 6000, evidence);
    }

    private static CameraMediaEvidence evidence(
            Instant start, Instant end, long first, long last, int samples, long gap) {
        return new CameraMediaEvidence(start, end, first, last, samples, gap);
    }

    private static LocalDateTime local(Instant instant) {
        return LocalDateTime.ofInstant(instant, ZONE);
    }
}
