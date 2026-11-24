package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

public record CameraDeviceDeleteVo(
        @Schema(description = "设备或独立通道标识") String groupKey,
        @Schema(description = "操作涉及的全部通道数") int channelCount,
        @Schema(description = "删除时是否一并清理未被其他资产使用的连接配置") boolean removesSource,
        @Schema(description = "提交删除时需要的变更校验值") String revision) {}
