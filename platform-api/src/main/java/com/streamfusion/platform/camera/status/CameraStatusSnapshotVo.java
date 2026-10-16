package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;

@Schema(description = "相机通道内各码流的分层事实与最新执行状态")
public record CameraStatusSnapshotVo(
        @Schema(description = "相机通道ID") String cameraId,
        @Schema(description = "响应生成时的服务器时间，UTC") Instant serverNow,
        @Schema(description = "当前通道的码流状态列表") List<Profile> profiles) {
    @Schema(name = "CameraStatusProfileVo", description = "单码流的连接与收流状态")
    public record Profile(
            @Schema(description = "码流ID") String streamProfileId,
            @Schema(description = "RTSP控制连接检查") CameraStatusLayerVo control,
            @Schema(description = "媒体接收检查，不证明解码或画面正常") CameraStatusLayerVo media,
            @Schema(description = "平台官方在线状态") CameraStatusLayerVo upstream) {}
}
