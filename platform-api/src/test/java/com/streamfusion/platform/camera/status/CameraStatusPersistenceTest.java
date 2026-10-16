package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.support.CameraTestSupport;
import java.time.LocalDateTime;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;

/** Verifies the MyBatis property-to-column mapping for the reserved SQL value field. */
class CameraStatusPersistenceTest extends CameraTestSupport {
    @Autowired private CameraStatusSnapshotMapper snapshots;

    @Test
    void snapshotValueUsesObservationValueColumnAndRoundTrips() throws Exception {
        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var camera = camera(auth, sourceId, "状态映射");
        long cameraId = camera.path("cameraId").asLong();
        long profileId = camera.path("profiles").get(0).path("streamProfileId").asLong();

        var now = LocalDateTime.now();
        var snapshot = new CameraStatusSnapshotEntity();
        snapshot.setProfileId(profileId);
        snapshot.setCameraId(cameraId);
        snapshot.setLayer("CONTROL");
        snapshot.setProbeKind("RTSP_DESCRIBE");
        snapshot.setValue("ACCESSIBLE");
        snapshot.setReasonCode("RTSP_ACCESSIBLE");
        snapshot.setObservedAt(now);
        snapshot.setReadAt(now);
        snapshot.setValidUntil(now.plusMinutes(5));
        snapshot.setCheckRequestedAt(now);
        snapshot.setCheckId(1L);
        snapshot.setConnectionKeyId("testkey");
        snapshot.setConnectionDigest(new byte[32]);
        snapshot.setUpdatedAt(now);

        snapshots.insert(snapshot);

        assertThat(
                        jdbc.queryForObject(
                                "SELECT observation_value FROM camera_status_snapshot WHERE profile_id=?",
                                String.class,
                                profileId))
                .isEqualTo("ACCESSIBLE");
        assertThat(snapshots.get(profileId, "CONTROL").getValue()).isEqualTo("ACCESSIBLE");
        snapshot.setLayer("MEDIA");
        snapshot.setProbeKind("ZLM_RTP_FRAMES");
        snapshot.setValue("FAILED");
        snapshot.setReasonCode("MEDIA_NO_FRAMES");
        snapshot.setMediaWindowStartedAt(now.minusSeconds(5));
        snapshot.setMediaWindowEndedAt(now);
        snapshot.setMediaFirstFrames(5L);
        snapshot.setMediaLastFrames(5L);
        snapshot.setMediaSampleCount(3);
        snapshot.setMediaMaxGapMs(5000L);
        snapshots.insert(snapshot);
        assertThat(snapshots.forCamera(cameraId)).hasSize(2);
        var media = snapshots.get(profileId, "MEDIA");
        assertThat(media.getValue()).isEqualTo("FAILED");
        assertThat(media.getMediaLastFrames()).isEqualTo(5);
        media.setLastAccessibleAt(now);
        snapshots.update(media);
        media.setLastAccessibleAt(null);
        snapshots.update(media);
        assertThat(snapshots.get(profileId, "MEDIA").getLastAccessibleAt()).isNull();
        assertThat(snapshots.get(profileId, "CONTROL").getValue()).isEqualTo("ACCESSIBLE");
    }
}
