package com.streamfusion.platform.camera.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "账户相机数据范围整体替换命令")
public record CameraScopeUpdateDto(
        @Schema(description = "当前授权聚合版本，尚未保存时为0") String version,
        @Schema(description = "显式视频组授权ID集合，最多200个，动态包含全部后代") List<String> groupIds,
        @Schema(description = "兼容相机通道ID集合；有关联设备时保存为设备授权，最多2000个") List<String> cameraIds,
        @Schema(description = "显式设备授权ID集合，最多2000个；旧客户端可不传") List<String> deviceIds) {}
