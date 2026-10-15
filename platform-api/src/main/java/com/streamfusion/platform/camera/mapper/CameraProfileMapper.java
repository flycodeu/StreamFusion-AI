package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;

@Mapper
public interface CameraProfileMapper extends BaseMapper<CameraProfileEntity> {
    @Select(
            "<script>SELECT p.* FROM camera_channel c JOIN camera_stream_profile p ON p.id=(SELECT sp.id FROM camera_stream_profile sp WHERE sp.channel_id=c.id AND sp.enabled=1 ORDER BY CASE WHEN sp.id=c.default_preview_profile_id THEN 0 WHEN sp.usage_hint='MAIN' THEN 1 ELSE 2 END,sp.id LIMIT 1) WHERE c.id IN <foreach collection='ids' item='id' open='(' separator=',' close=')'>#{id}</foreach> ORDER BY c.id</script>")
    List<CameraProfileEntity> statusRepresentatives(@Param("ids") List<Long> ids);

    @Select(
            "<script>SELECT * FROM camera_stream_profile WHERE channel_id IN "
                    + "<foreach collection='ids' item='id' open='(' separator=',' close=')'>#{id}</foreach> "
                    + "ORDER BY channel_id,id LIMIT #{limit}</script>")
    List<CameraProfileEntity> statusProfiles(
            @Param("ids") List<Long> ids, @Param("limit") int limit);
}
