package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;

@Mapper
public interface CameraStatusCheckMapper extends BaseMapper<CameraStatusCheckEntity> {
    @Select("SELECT * FROM camera_status_check WHERE id=#{id} FOR UPDATE")
    CameraStatusCheckEntity lock(long id);

    @Select(
            "SELECT * FROM camera_status_check WHERE actor_user_id=#{userId} AND client_request_id=#{key}")
    CameraStatusCheckEntity byRequest(@Param("userId") long userId, @Param("key") String key);

    @Select(
            "SELECT c.* FROM camera_status_check c WHERE c.camera_id=#{cameraId} AND NOT EXISTS "
                    + "(SELECT 1 FROM camera_status_check n WHERE n.profile_id=c.profile_id AND n.probe_kind=c.probe_kind "
                    + "AND (n.requested_at>c.requested_at OR (n.requested_at=c.requested_at AND n.id>c.id)))")
    List<CameraStatusCheckEntity> latest(long cameraId);

    @Select(
            "<script>SELECT c.* FROM camera_status_check c WHERE c.profile_id IN "
                    + "<foreach collection='ids' item='id' open='(' separator=',' close=')'>#{id}</foreach> "
                    + "AND NOT EXISTS (SELECT 1 FROM camera_status_check n WHERE n.profile_id=c.profile_id AND n.probe_kind=c.probe_kind "
                    + "AND (n.requested_at&gt;c.requested_at OR (n.requested_at=c.requested_at AND n.id&gt;c.id)))</script>")
    List<CameraStatusCheckEntity> latestForProfiles(@Param("ids") List<Long> ids);

    @Select(
            "SELECT id FROM camera_status_check WHERE execution_state IN ('QUEUED','RUNNING') "
                    + "AND deadline_at<=#{now} ORDER BY deadline_at,id LIMIT 100")
    List<Long> expired(LocalDateTime now);

    @Select(
            "SELECT id FROM camera_status_check WHERE execution_state NOT IN ('QUEUED','RUNNING') "
                    + "AND retain_until<=#{now} ORDER BY retain_until,id LIMIT 100")
    List<Long> prunable(LocalDateTime now);

    @Select(
            "SELECT id FROM camera_status_check WHERE execution_state IN ('QUEUED','RUNNING') "
                    + "AND requested_at<=#{cutoff} ORDER BY requested_at,id LIMIT 100")
    List<Long> activeBefore(LocalDateTime cutoff);
}
