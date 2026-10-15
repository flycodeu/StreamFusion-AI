package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;

@Schema(description = "相机通道内各码流的控制层事实与最新执行状态")
public record CameraStatusSnapshotVo(
        @Schema(description = "相机通道ID") String cameraId,
        @Schema(description = "响应生成时的服务器时间，UTC") Instant serverNow,
        @Schema(description = "当前通道的码流状态列表") List<Profile> profiles) {
    @Schema(name = "CameraStatusProfileVo", description = "单码流状态，观察事实与检查执行分离")
    public record Profile(
            @Schema(description = "码流ID") String streamProfileId,
            @Schema(description = "状态层，本期固定CONTROL") String layer,
            @Schema(description = "事实来源，本期固定RTSP_DESCRIBE") String probeKind,
            @Schema(description = "最新合法观察事实，尚未检查为空", nullable = true)
                    CameraStatusObservationVo observation,
            @Schema(description = "新鲜度：NEVER/CURRENT/STALE") String freshness,
            @Schema(description = "该事实是否仍匹配当前实际连接语义") boolean configurationCurrent,
            @Schema(description = "该码流最近一次检查回执，无记录为空", nullable = true)
                    CameraStatusCheckVo latestCheck,
            @Schema(description = "当前配置是否允许新手动检查，后端仍复核执行资格") boolean canCheck,
            @Schema(description = "阻止新检查的受控原因码，可检查时为空", nullable = true) String blockingReasonCode,
            @Schema(description = "连接采样时间；配置变化或无事实时为空", nullable = true)
                    CameraStatusTimelineVo timeline) {}
}
