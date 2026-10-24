package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface CameraProfileMapper extends BaseMapper<CameraProfileEntity> {
    List<CameraProfileEntity> statusRepresentatives(@Param("ids") List<Long> ids);

    List<CameraProfileEntity> statusProfiles(
            @Param("ids") List<Long> ids, @Param("limit") int limit);
}
