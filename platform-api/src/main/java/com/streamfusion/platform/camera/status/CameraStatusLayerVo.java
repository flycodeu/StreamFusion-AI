package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "单层观察事实与检查执行状态")
public record CameraStatusLayerVo(
        @Schema(description = "CONTROL/MEDIA/UPSTREAM") CameraStatusLayer layer,
        @Schema(description = "RTSP_DESCRIBE/ZLM_RTP_FRAMES/HIK_PLATFORM_ONLINE") String probeKind,
        @Schema(description = "最近合法事实，未检查为空", nullable = true)
                CameraStatusObservationVo observation,
        @Schema(description = "NEVER未检查/CURRENT最近结果/STALE连接配置已变化；不按时间失效") String freshness,
        @Schema(description = "事实是否仍匹配当前连接配置") boolean configurationCurrent,
        @Schema(description = "本层最新检查回执", nullable = true) CameraStatusCheckVo latestCheck,
        @Schema(description = "当前配置允许新检查，执行时仍复核权限") boolean canCheck,
        @Schema(description = "阻止新检查的原因", nullable = true) String blockingReasonCode,
        @Schema(description = "本层同一配置下的采样时间", nullable = true) CameraStatusTimelineVo timeline) {}
