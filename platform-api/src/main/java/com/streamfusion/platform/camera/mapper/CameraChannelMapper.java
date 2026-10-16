package com.streamfusion.platform.camera.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.baomidou.mybatisplus.core.metadata.IPage;
import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.service.CameraAccessService.Visibility;
import com.streamfusion.platform.camera.status.CameraStatusPageRow;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;
import org.apache.ibatis.annotations.Select;

@Mapper
public interface CameraChannelMapper extends BaseMapper<CameraChannelEntity> {
    List<CameraStatusPageRow> statusSelectedChannels(
            @Param("ids") List<Long> ids, @Param("visibility") Visibility visibility);

    List<CameraStatusPageRow> statusGroupRepresentatives(
            @Param("groupIds") List<Long> groupIds,
            @Param("visibility") Visibility visibility,
            @Param("limit") int limit);

    record Filter(
            String name, Long sourceId, String adapterType, String lifecycle, List<Long> groups) {}

    IPage<CameraAssetRow> pageVisible(
            Page<CameraAssetRow> page,
            @Param("filter") Filter filter,
            @Param("visibility") Visibility visibility);

    IPage<CameraDeviceGroupRow> pageDeviceGroups(
            Page<CameraDeviceGroupRow> page,
            @Param("filter") Filter filter,
            @Param("visibility") Visibility visibility);

    IPage<CameraAssetRow> pageDeviceChannels(
            Page<CameraAssetRow> page,
            @Param("groupKey") String groupKey,
            @Param("filter") Filter filter,
            @Param("visibility") Visibility visibility);

    IPage<CameraStatusPageRow> pageStatus(
            Page<CameraStatusPageRow> page,
            @Param("name") String name,
            @Param("observation") String observation,
            @Param("layer") String layer,
            @Param("visibility") Visibility visibility);

    CameraAssetRow visible(@Param("id") long id, @Param("visibility") Visibility visibility);

    List<CameraStatusPageRow> statusDeviceRepresentatives(
            @Param("keys") List<String> keys,
            @Param("visibility") Visibility visibility,
            @Param("limit") int limit);

    @Select("SELECT COUNT(*) FROM camera_user_channel_grant WHERE channel_id=#{id}")
    long directGrantCount(@Param("id") long id);
}
