package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "设备列表的授权投影；计数仅含当前筛选和授权范围内的通道")
public record CameraDeviceGroupVo(
        @Schema(description = "设备集合标识；d前缀表示已关联设备，c前缀表示尚未关联设备的独立通道") String groupKey,
        @Schema(description = "设备显示名称；未关联设备时显示通道名称") String name,
        @Schema(description = "是否已有可靠的设备归属，不表示设备在线状态") boolean identified,
        @Schema(description = "设备报告的制造商", nullable = true) String manufacturer,
        @Schema(description = "设备报告的型号", nullable = true) String model,
        @Schema(description = "接入来源的显示名称") String sourceDisplayName,
        @Schema(description = "已识别的接入适配器类型", nullable = true) String sourceType,
        @Schema(description = "连接类别：DEVICE设备、PLATFORM平台或RTSP地址") String connectionCategory,
        @Schema(description = "当前筛选及授权范围内的通道总数") long channelCount,
        @Schema(description = "当前筛选及授权范围内已启用的通道数") long enabledCount,
        @Schema(description = "当前筛选及授权范围内已停用的通道数") long disabledCount,
        @Schema(description = "当前筛选及授权范围内待归档的通道数") long pendingCount,
        @Schema(description = "当前可见通道的归属数量，待归档也计为一种归属") long groupCount,
        @Schema(description = "仅有一个已归档分组时的完整路径，其余为空", nullable = true) String groupPath) {}
