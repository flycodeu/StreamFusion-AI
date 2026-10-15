package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraEndpointEntity;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;

@Mapper
public interface CameraEndpointMapper extends BaseMapper<CameraEndpointEntity> {
    @Select(
            "<script>SELECT * FROM camera_source_endpoint WHERE source_id IN "
                    + "<foreach collection='ids' item='id' open='(' separator=',' close=')'>#{id}</foreach> "
                    + "</script>")
    List<CameraEndpointEntity> selectBySources(@Param("ids") List<Long> ids);
}
