package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.temporal.ChronoUnit;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Projects accepted observations inside the service's existing locked completion transaction. */
@Component
@RequiredArgsConstructor
public class CameraStatusFactWriter {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraStatusSnapshotMapper snapshots;
    private final CameraRtspTargetService targets;
    private final CameraStatusProperties properties;
    private final Clock clock;

    CameraStatusSnapshotEntity prepareObservation(
            CameraStatusCheckEntity check, CameraProbeResult result) {
        check.setResultValue(result.outcome().name());
        check.setObservedAt(now());
        check.setReadAt(check.getObservedAt());
        if ("MEDIA".equals(check.getLayer())) setMediaEvidence(check, result.evidence());
        check.setValidUntil(
                check.getObservedAt().plusNanos(properties.getFreshnessMs() * 1_000_000));
        var previous = snapshots.get(check.getProfileId(), check.getLayer());
        boolean applies =
                previous == null
                        || previous.getCheckRequestedAt().isBefore(check.getRequestedAt())
                        || previous.getCheckRequestedAt().equals(check.getRequestedAt())
                                && previous.getCheckId() < check.getId();
        check.setSnapshotApplied(applies);
        return previous;
    }

    void writeSnapshot(
            CameraStatusCheckEntity check,
            CameraStatusSnapshotEntity previous,
            CameraChannelEntity camera,
            CameraProfileEntity profile) {
        var snapshot = new CameraStatusSnapshotEntity();
        snapshot.setProfileId(check.getProfileId());
        snapshot.setCameraId(check.getCameraId());
        snapshot.setLayer(check.getLayer());
        snapshot.setProbeKind(check.getProbeKind());
        snapshot.setValue(check.getResultValue());
        snapshot.setReasonCode(check.getReasonCode());
        snapshot.setObservedAt(check.getObservedAt());
        snapshot.setReadAt(check.getReadAt());
        snapshot.setValidUntil(check.getValidUntil());
        snapshot.setMediaWindowStartedAt(check.getMediaWindowStartedAt());
        snapshot.setMediaWindowEndedAt(check.getMediaWindowEndedAt());
        snapshot.setMediaFirstFrames(check.getMediaFirstFrames());
        snapshot.setMediaLastFrames(check.getMediaLastFrames());
        snapshot.setMediaSampleCount(check.getMediaSampleCount());
        snapshot.setMediaMaxGapMs(check.getMediaMaxGapMs());
        snapshot.setCheckRequestedAt(check.getRequestedAt());
        snapshot.setCheckId(check.getId());
        snapshot.setConnectionKeyId(check.getConnectionKeyId());
        snapshot.setConnectionDigest(check.getConnectionDigest());
        recordTimeline(snapshot, previous, camera, profile);
        snapshot.setUpdatedAt(now());
        if (previous == null) snapshots.insert(snapshot);
        else snapshots.update(snapshot);
    }

    private void recordTimeline(
            CameraStatusSnapshotEntity next,
            CameraStatusSnapshotEntity previous,
            CameraChannelEntity camera,
            CameraProfileEntity profile) {
        boolean sameConnection = false;
        if (previous != null) {
            var original =
                    targets.inspect(camera, profile, previous.getConnectionKeyId(), () -> {});
            sameConnection =
                    original.connectionDigest() != null
                            && MessageDigest.isEqual(
                                    previous.getConnectionDigest(), original.connectionDigest());
        }
        var prior = sameConnection ? CameraStatusTimelineVo.from(previous) : null;
        next.setStateSince(
                sameConnection && Objects.equals(previous.getValue(), next.getValue())
                        ? LocalDateTime.ofInstant(prior.stateSince(), ZONE)
                        : next.getObservedAt());
        next.setLastAccessibleAt(
                "ACCESSIBLE".equals(next.getValue())
                        ? next.getObservedAt()
                        : prior == null || prior.lastAccessibleAt() == null
                                ? null
                                : LocalDateTime.ofInstant(prior.lastAccessibleAt(), ZONE));
        next.setLastFailedAt(
                "FAILED".equals(next.getValue())
                        ? next.getObservedAt()
                        : prior == null || prior.lastFailedAt() == null
                                ? null
                                : LocalDateTime.ofInstant(prior.lastFailedAt(), ZONE));
    }

    private static void setMediaEvidence(
            CameraStatusCheckEntity check, CameraMediaEvidence evidence) {
        if (evidence == null) return;
        check.setMediaWindowStartedAt(LocalDateTime.ofInstant(evidence.windowStartedAt(), ZONE));
        check.setMediaWindowEndedAt(LocalDateTime.ofInstant(evidence.windowEndedAt(), ZONE));
        check.setMediaFirstFrames(evidence.firstFrames());
        check.setMediaLastFrames(evidence.lastFrames());
        check.setMediaSampleCount(evidence.sampleCount());
        check.setMediaMaxGapMs(evidence.maxGapMs());
    }

    boolean validMediaFact(CameraStatusCheckEntity check, CameraProbeResult result) {
        var evidence = result.evidence();
        if (evidence == null
                || evidence.windowStartedAt() == null
                || evidence.windowEndedAt() == null) return false;
        boolean consistentResult =
                result.outcome() == CameraProbeResult.Outcome.ACCESSIBLE
                                && "MEDIA_RECEIVING".equals(result.reasonCode())
                                && evidence.lastFrames() > evidence.firstFrames()
                        || result.outcome() == CameraProbeResult.Outcome.FAILED
                                && "MEDIA_NO_FRAMES".equals(result.reasonCode())
                                && evidence.lastFrames() == evidence.firstFrames();
        return consistentResult
                && Duration.between(evidence.windowStartedAt(), evidence.windowEndedAt()).toMillis()
                        >= 5_000
                && !evidence.windowStartedAt()
                        .truncatedTo(ChronoUnit.MILLIS)
                        .isBefore(instant(check.getStartedAt()))
                && !evidence.windowEndedAt().truncatedTo(ChronoUnit.MILLIS).isAfter(nowInstant())
                && evidence.firstFrames() >= 0
                && evidence.lastFrames() >= evidence.firstFrames()
                && evidence.sampleCount() >= 3
                && evidence.maxGapMs() >= 0;
    }

    private Instant nowInstant() {
        return clock.instant().truncatedTo(ChronoUnit.MILLIS);
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(nowInstant(), ZONE);
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZONE).toInstant();
    }
}
