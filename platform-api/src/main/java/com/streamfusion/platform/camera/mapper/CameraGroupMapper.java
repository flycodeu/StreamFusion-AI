package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import java.util.List;
import org.apache.ibatis.annotations.*;

@Mapper
public interface CameraGroupMapper extends BaseMapper<CameraGroupEntity> {
    @Select("SELECT * FROM camera_group ORDER BY sort_order,id LIMIT 1001")
    List<CameraGroupEntity> allBounded();

    @Select(
            "<script>SELECT COUNT(*) FROM camera_group WHERE name=#{name} AND <choose><when test='parentId != null'>parent_id=#{parentId}</when><otherwise>parent_id IS NULL</otherwise></choose><if test='excludeId != null'> AND id != #{excludeId}</if></script>")
    long sameName(
            @Param("parentId") Long parentId,
            @Param("name") String name,
            @Param("excludeId") Long excludeId);

    @Select("SELECT COUNT(*) FROM camera_channel WHERE group_id=#{id}")
    long cameras(@Param("id") long id);

    @Select("SELECT COUNT(*) FROM camera_user_group_grant WHERE group_id=#{id}")
    long grants(@Param("id") long id);
}
