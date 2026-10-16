package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;

@Mapper
public interface CameraStatusPlanMapper extends BaseMapper<CameraStatusPlanEntity> {
    @Select("SELECT * FROM camera_status_plan WHERE id=#{id} FOR UPDATE")
    CameraStatusPlanEntity lock(long id);

    @Select(
            "SELECT * FROM camera_status_plan WHERE actor_user_id=#{actor} AND client_request_id=#{key}")
    CameraStatusPlanEntity byRequest(@Param("actor") long actor, @Param("key") String key);

    @Select(
            """
            SELECT COALESCE(SUM(units * 1.0 / interval_minutes),0) FROM (
              SELECT p.id,p.interval_minutes,
                CASE WHEN s.adapter_type='HIK_PLATFORM' THEN CEIL(COUNT(*) / 100.0) ELSE COUNT(*) END units
              FROM camera_status_plan p JOIN camera_status_plan_target t ON t.plan_id=p.id
              JOIN camera_stream_profile f ON f.id=t.profile_id JOIN camera_source s ON s.id=f.source_id
              WHERE p.state='ACTIVE' AND p.end_at>#{now}
              GROUP BY p.id,p.interval_minutes,f.source_id,s.adapter_type,
                CASE WHEN s.adapter_type='HIK_PLATFORM' THEN t.next_at ELSE NULL END
            ) rates
            """)
    double requestsPerMinute(LocalDateTime now);

    @Select(
            "SELECT COALESCE(SUM(media_target_count * 1.0 / interval_minutes),0) FROM camera_status_plan WHERE state='ACTIVE' AND layer='MEDIA' AND end_at>#{now}")
    double mediaTargetsPerMinute(LocalDateTime now);

    @Select(
            "SELECT COUNT(DISTINCT CASE WHEN layer='UPSTREAM' THEN CONCAT('s',source_id) ELSE CONCAT('c',id) END) FROM camera_status_check WHERE plan_id=#{planId} AND execution_state IN ('QUEUED','RUNNING') AND deadline_at>#{now}")
    int pending(@Param("planId") long planId, @Param("now") LocalDateTime now);

    @Select(
            "SELECT COUNT(DISTINCT CASE WHEN layer='UPSTREAM' THEN CONCAT('p',COALESCE(plan_id,id),':',source_id) ELSE CONCAT('c',id) END) FROM camera_status_check WHERE execution_state IN ('QUEUED','RUNNING') AND deadline_at>#{now}")
    int pendingRequests(LocalDateTime now);

    @Select(
            "SELECT COUNT(*) FROM camera_status_check WHERE source_id=#{sourceId} AND layer='UPSTREAM' AND execution_state IN ('QUEUED','RUNNING') AND deadline_at>#{now}")
    int pendingSource(@Param("sourceId") long sourceId, @Param("now") LocalDateTime now);

    @Select(
            """
            SELECT p.*,
              (SELECT COUNT(*) FROM camera_status_plan_target t WHERE t.plan_id=p.id AND t.blocked=1) blocked_count,
              (SELECT MIN(t.next_at) FROM camera_status_plan_target t WHERE t.plan_id=p.id AND t.blocked=0 AND t.next_at<p.end_at) next_at,
              (SELECT COALESCE(t.reason_code,c.reason_code) FROM camera_status_plan_target t
                LEFT JOIN camera_status_check c ON c.id=t.last_check_id
                WHERE t.plan_id=p.id AND (t.reason_code IS NOT NULL OR c.execution_state IN ('SKIPPED','CANCELLED','INTERRUPTED'))
                ORDER BY t.next_at DESC,t.id DESC LIMIT 1) last_reason_code,
              progress.success_count,progress.failed_count,progress.pending_count,progress.skipped_count,progress.unknown_count
            FROM camera_status_plan p LEFT JOIN (
                SELECT t.plan_id,
                  SUM(CASE WHEN t.reason_code IS NULL AND c.execution_state='COMPLETED' AND c.result_value='ACCESSIBLE' THEN 1 ELSE 0 END) success_count,
                  SUM(CASE WHEN t.reason_code IS NULL AND c.execution_state='COMPLETED' AND c.result_value='FAILED' THEN 1 ELSE 0 END) failed_count,
                  SUM(CASE WHEN t.reason_code IS NULL AND c.execution_state IN ('QUEUED','RUNNING') THEN 1 ELSE 0 END) pending_count,
                  SUM(CASE WHEN t.reason_code IS NOT NULL OR c.execution_state IN ('SKIPPED','CANCELLED','INTERRUPTED') THEN 1 ELSE 0 END) skipped_count,
                  SUM(CASE WHEN t.reason_code IS NULL AND (c.id IS NULL OR (c.execution_state='COMPLETED' AND c.result_value='UNSUPPORTED')) THEN 1 ELSE 0 END) unknown_count
                FROM camera_status_plan_target t LEFT JOIN camera_status_check c ON c.id=t.last_check_id
                JOIN camera_status_plan owned ON owned.id=t.plan_id AND owned.actor_user_id=#{actor}
                GROUP BY t.plan_id
            ) progress ON progress.plan_id=p.id WHERE p.actor_user_id=#{actor}
            ORDER BY p.created_at DESC,p.id DESC LIMIT 100
            """)
    List<CameraStatusPlanEntity> forActor(long actor);
}
