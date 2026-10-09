package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.math.BigDecimal;
import java.time.Instant;
import java.util.Map;

@Schema(description = "通道码流档案与最近可靠观测摘要")
public record CameraProfileVo(
        @Schema(description = "稳定码流ID") String streamProfileId,
        @Schema(description = "本地显示标签") String label,
        @Schema(description = "码流用途：MAIN/SUB/THIRD/CUSTOM/UNKNOWN") String usageHint,
        @Schema(description = "用途来源：DEVICE_REPORTED/MANUAL/UNKNOWN") String usageOrigin,
        @Schema(description = "是否允许新使用") boolean enabled,
        @Schema(description = "观测视频编码，未知为空", nullable = true) String videoCodec,
        @Schema(description = "观测像素宽，未知为空", nullable = true) Integer width,
        @Schema(description = "观测像素高，未知为空", nullable = true) Integer height,
        @Schema(description = "观测帧率，单位fps，可为小数", nullable = true) BigDecimal frameRate,
        @Schema(description = "观测或设备声明码率，单位kbps", nullable = true) Long bitrateKbps,
        @Schema(description = "观测音频编码，未知为空", nullable = true) String audioCodec,
        @Schema(description = "是否有音轨，null表示未知", nullable = true) Boolean hasAudio,
        @Schema(description = "参数可靠观测时间，UTC", nullable = true) Instant parametersObservedAt,
        @Schema(description = "参数来源：CATALOG设备声明或MEDIA实际媒体观测", nullable = true)
                String parametersOrigin,
        @Schema(description = "标签、用途、启停及定位配置共享编辑版本") String version,
        @Schema(description = "非秘密定位摘要，不含路径、查询参数或凭据") Map<String, Object> locatorSummary,
        @Schema(description = "有效用途及依据；NAME_RULE为名称推断，原始用途和人工设置不变")
                com.streamfusion.platform.camera.service.CameraStreamClassification.Result
                        classification) {}
