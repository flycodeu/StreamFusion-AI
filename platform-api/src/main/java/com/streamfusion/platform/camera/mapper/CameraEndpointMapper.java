package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraEndpointEntity;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;

@Mapper
public interface CameraEndpointMapper extends BaseMapper<CameraEndpointEntity> {
    /** Safe address projection only; callers pass sources from their already-authorized page. */
    record DisplayAddress(Long sourceId, String category, String host, Integer port) {}

    @Select(
            """
            <script>
            SELECT s.id AS source_id,s.connection_category AS category,e.host,e.port
            FROM camera_source s JOIN camera_source_endpoint e ON e.source_id=s.id
            WHERE s.id IN
              <foreach collection='ids' item='id' open='(' separator=',' close=')'>#{id}</foreach>
              AND ((s.connection_category='PLATFORM' AND e.purpose='PLATFORM_HTTP')
                OR (s.connection_category='RTSP' AND e.purpose='RTSP')
                OR (s.connection_category='STREAM' AND e.purpose='STREAM')
                OR (s.connection_category='DEVICE' AND e.purpose IN ('DEVICE_HTTP','VENDOR_HTTP','ONVIF','RTSP')))
            ORDER BY s.id,CASE
              WHEN s.connection_category='PLATFORM' AND e.purpose='PLATFORM_HTTP' THEN 0
              WHEN s.connection_category='RTSP' AND e.purpose='RTSP' THEN 0
              WHEN s.connection_category='DEVICE' AND e.purpose='DEVICE_HTTP' THEN 0
              WHEN s.adapter_type='ONVIF' AND e.purpose='ONVIF' THEN 1
              WHEN e.purpose='VENDOR_HTTP' THEN 2
              WHEN e.purpose='ONVIF' THEN 3
              WHEN e.purpose='RTSP' THEN 4 ELSE 5 END,e.id
            </script>
            """)
    List<DisplayAddress> displayAddresses(@Param("ids") List<Long> ids);

    @Select(
            "<script>SELECT * FROM camera_source_endpoint WHERE source_id IN "
                    + "<foreach collection='ids' item='id' open='(' separator=',' close=')'>#{id}</foreach> "
                    + "</script>")
    List<CameraEndpointEntity> selectBySources(@Param("ids") List<Long> ids);
}
