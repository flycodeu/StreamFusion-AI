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
            "SELECT t.* FROM camera_status_plan_target t JOIN camera_status_plan p ON p.id=t.plan_id WHERE p.state='ACTIVE' AND p.end_at>#{now} AND t.blocked=0 AND t.next_at<=#{now} ORDER BY t.next_at,t.id LIMIT 1")
    List<CameraStatusPlanTargetEntity> due(LocalDateTime now);

    @Select("SELECT * FROM camera_status_plan_target WHERE id=#{id} FOR UPDATE")
    CameraStatusPlanTargetEntity lock(long id);
}
