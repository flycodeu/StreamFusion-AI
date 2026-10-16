package com.streamfusion.platform.camera.status;

import java.util.List;
import org.apache.ibatis.annotations.Insert;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;
import org.apache.ibatis.annotations.Update;

@Mapper
public interface CameraStatusSnapshotMapper {
    String COLUMNS =
            "profile_id,camera_id,layer,probe_kind,observation_value AS `value`,reason_code,"
                    + "observed_at,state_since,last_accessible_at,last_failed_at,read_at,valid_until,"
                    + "media_window_started_at,media_window_ended_at,media_first_frames,media_last_frames,"
                    + "media_sample_count,media_max_gap_ms,check_requested_at,check_id,connection_key_id,connection_digest,updated_at";

    @Select(
            "SELECT "
                    + COLUMNS
                    + " FROM camera_status_snapshot WHERE profile_id=#{profileId} AND layer=#{layer}")
    CameraStatusSnapshotEntity get(
            @Param("profileId") long profileId, @Param("layer") String layer);

    @Select("SELECT " + COLUMNS + " FROM camera_status_snapshot WHERE camera_id=#{cameraId}")
    List<CameraStatusSnapshotEntity> forCamera(long cameraId);

    @Select(
            "<script>SELECT "
                    + COLUMNS
                    + " FROM camera_status_snapshot WHERE profile_id IN "
                    + "<foreach collection='ids' item='id' open='(' separator=',' close=')'>#{id}</foreach></script>")
    List<CameraStatusSnapshotEntity> forProfiles(@Param("ids") List<Long> ids);

    @Insert(
            """
            INSERT INTO camera_status_snapshot(profile_id,camera_id,layer,probe_kind,observation_value,
            reason_code,observed_at,state_since,last_accessible_at,last_failed_at,read_at,valid_until,
            media_window_started_at,media_window_ended_at,media_first_frames,media_last_frames,
            media_sample_count,media_max_gap_ms,check_requested_at,check_id,connection_key_id,connection_digest,updated_at)
            VALUES(#{profileId},#{cameraId},#{layer},#{probeKind},#{value},#{reasonCode},#{observedAt},
            #{stateSince},#{lastAccessibleAt},#{lastFailedAt},#{readAt},#{validUntil},
            #{mediaWindowStartedAt},#{mediaWindowEndedAt},#{mediaFirstFrames},#{mediaLastFrames},
            #{mediaSampleCount},#{mediaMaxGapMs},#{checkRequestedAt},#{checkId},#{connectionKeyId},#{connectionDigest},#{updatedAt})
            """)
    int insert(CameraStatusSnapshotEntity snapshot);

    @Update(
            """
            UPDATE camera_status_snapshot SET camera_id=#{cameraId},probe_kind=#{probeKind},
            observation_value=#{value},reason_code=#{reasonCode},observed_at=#{observedAt},
            state_since=#{stateSince},last_accessible_at=#{lastAccessibleAt},last_failed_at=#{lastFailedAt},
            read_at=#{readAt},valid_until=#{validUntil},media_window_started_at=#{mediaWindowStartedAt},
            media_window_ended_at=#{mediaWindowEndedAt},media_first_frames=#{mediaFirstFrames},
            media_last_frames=#{mediaLastFrames},media_sample_count=#{mediaSampleCount},media_max_gap_ms=#{mediaMaxGapMs},
            check_requested_at=#{checkRequestedAt},check_id=#{checkId},connection_key_id=#{connectionKeyId},
            connection_digest=#{connectionDigest},updated_at=#{updatedAt}
            WHERE profile_id=#{profileId} AND layer=#{layer}
            """)
    int update(CameraStatusSnapshotEntity snapshot);
}
