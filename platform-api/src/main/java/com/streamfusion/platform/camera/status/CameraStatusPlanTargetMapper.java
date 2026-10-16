package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Select;

@Mapper
public interface CameraStatusPlanTargetMapper extends BaseMapper<CameraStatusPlanTargetEntity> {
    @Select(
            "SELECT DISTINCT CASE WHEN c.device_id IS NULL THEN CONCAT('c',c.id) ELSE CONCAT('d',c.device_id) END FROM camera_status_plan_target t JOIN camera_channel c ON c.id=t.camera_id WHERE t.plan_id=#{planId}")
    List<String> groupKeys(long planId);

    @Select(
            """
            SELECT t.* FROM camera_status_plan_target t
              JOIN camera_status_plan p ON p.id=t.plan_id
              LEFT JOIN camera_stream_profile f ON f.id=t.profile_id
              LEFT JOIN camera_source s ON s.id=f.source_id
            WHERE p.state='ACTIVE' AND p.end_at>#{now} AND t.blocked=0 AND t.next_at<=#{now}
              AND (s.adapter_type IS NULL OR s.adapter_type<>'HIK_PLATFORM' OR NOT EXISTS (
                SELECT 1 FROM camera_status_check c WHERE c.source_id=f.source_id
                  AND c.layer='UPSTREAM' AND c.execution_state IN ('QUEUED','RUNNING')
                  AND c.deadline_at>#{now}))
            ORDER BY t.next_at,t.id LIMIT 16
            """)
    List<CameraStatusPlanTargetEntity> due(LocalDateTime now);

    @Select(
            """
            SELECT t.* FROM camera_status_plan_target t
              JOIN camera_stream_profile f ON f.id=t.profile_id
              JOIN camera_stream_profile seed ON seed.id=#{profileId} AND seed.source_id=f.source_id
              JOIN camera_source s ON s.id=f.source_id AND s.adapter_type='HIK_PLATFORM'
            WHERE t.plan_id=#{planId} AND t.blocked=0 AND t.next_at<=#{now}
            ORDER BY t.next_at,t.id LIMIT 100
            """)
    List<CameraStatusPlanTargetEntity> duePlatformBatch(
            long planId, long profileId, LocalDateTime now);

    @Select("SELECT * FROM camera_status_plan_target WHERE id=#{id} FOR UPDATE")
    CameraStatusPlanTargetEntity lock(long id);

    @Select("SELECT * FROM camera_status_plan_target WHERE plan_id=#{planId} ORDER BY id")
    List<CameraStatusPlanTargetEntity> forPlan(long planId);
}
