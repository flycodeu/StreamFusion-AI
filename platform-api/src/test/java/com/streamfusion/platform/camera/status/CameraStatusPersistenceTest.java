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
        assertThat(snapshots.selectById(profileId).getValue()).isEqualTo("ACCESSIBLE");
    }
}
