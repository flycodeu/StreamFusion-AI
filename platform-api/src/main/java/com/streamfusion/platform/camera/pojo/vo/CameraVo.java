package com.streamfusion.platform.camera.pojo.vo;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
@Schema(description = "当前账户可见的相机资料；普通账号省略来源管理字段")
public record CameraVo(
        @Schema(description = "稳定相机通道ID") String cameraId,
        @Schema(description = "本地相机名称") String name,
        @Schema(description = "本地备注", nullable = true) String remark,
        @Schema(description = "上游通道名称，未观测时省略", nullable = true) String sourceName,
        @Schema(description = "接入来源当前显示名称") String sourceDisplayName,
        @Schema(description = "视频分组ID，待归档时省略", nullable = true) String groupId,
        @Schema(description = "视频分组完整路径，待归档时省略", nullable = true) String groupPath,
        @Schema(description = "接入来源的适配方式，未知时省略", nullable = true) String sourceType,
        @Schema(description = "连接类别DEVICE、PLATFORM或RTSP") String connectionCategory,
        @Schema(description = "人工厂商提示，不作为观测制造商", nullable = true) String vendorHint,
        @Schema(description = "该通道关联设备的最小观测摘要", nullable = true) DeviceSummary deviceSummary,
        @Schema(description = "生命周期：PENDING_ASSIGNMENT/ENABLED/DISABLED") String lifecycle,
        @Schema(description = "明确选定的默认码流ID，无默认时省略", nullable = true) String defaultPreviewProfileId,
        @Schema(description = "相机资料编辑版本") String version,
        @Schema(description = "创建时间，UTC") Instant createdAt,
        @Schema(description = "本地编辑更新时间，UTC") Instant updatedAt,
        @Schema(description = "接入来源ID，仅超管返回", nullable = true) String sourceId,
        @Schema(description = "接入来源当前编辑版本，仅超管返回", nullable = true) String sourceVersion,
        @Schema(description = "本地设备ID，仅超管且已关联设备时返回", nullable = true) String deviceId,
        @Schema(description = "来源内稳定通道键，仅超管返回", nullable = true) String externalChannelKey,
        @Schema(description = "码流档案列表，仅详情返回", nullable = true) List<CameraProfileVo> profiles) {
    @Schema(description = "授权通道可见的设备最小信息，不含连接配置")
    public record DeviceSummary(
            @Schema(description = "设备类型：IPC/NVR/DVR/ENCODER/UNKNOWN") String deviceType,
            @Schema(description = "可靠观测制造商，未知为空", nullable = true) String manufacturer,
            @Schema(description = "可靠观测型号，未知为空", nullable = true) String model) {}
}
