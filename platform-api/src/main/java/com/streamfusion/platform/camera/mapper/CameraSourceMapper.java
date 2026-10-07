package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraSourceEntity;
import org.apache.ibatis.annotations.Mapper;

@Mapper
public interface CameraSourceMapper extends BaseMapper<CameraSourceEntity> {
    @org.apache.ibatis.annotations.Select(
            "SELECT COUNT(*) FROM camera_channel WHERE source_id = #{sourceId}")
    long channelCount(long sourceId);

    @org.apache.ibatis.annotations.Select(
            "SELECT COUNT(*) FROM camera_channel c WHERE c.source_id=#{sourceId} "
                    + "AND (c.mapping_origin='ADAPTER' OR c.device_id IS NOT NULL "
                    + "OR EXISTS(SELECT 1 FROM camera_stream_profile p WHERE p.channel_id=c.id))")
    long boundChannelCount(long sourceId);

    @org.apache.ibatis.annotations.Select(
            "<script>SELECT DISTINCT c.source_id FROM camera_channel c WHERE c.source_id IN "
                    + "<foreach collection='sourceIds' item='id' open='(' separator=',' close=')'>#{id}</foreach> "
                    + "AND (c.mapping_origin='ADAPTER' OR c.device_id IS NOT NULL "
                    + "OR EXISTS(SELECT 1 FROM camera_stream_profile p WHERE p.channel_id=c.id))</script>")
    java.util.Set<Long> sourcesWithBoundChannels(
            @org.apache.ibatis.annotations.Param("sourceIds") java.util.List<Long> sourceIds);

    @org.apache.ibatis.annotations.Select(
            "<script>SELECT DISTINCT source_id FROM camera_channel WHERE source_id IN <foreach collection='sourceIds' item='id' open='(' separator=',' close=')'>#{id}</foreach></script>")
    java.util.Set<Long> sourcesWithChannels(
            @org.apache.ibatis.annotations.Param("sourceIds") java.util.List<Long> sourceIds);

    @org.apache.ibatis.annotations.Select(
            "SELECT COUNT(*) FROM camera_device WHERE source_id = #{sourceId}")
    long deviceCount(long sourceId);
}
