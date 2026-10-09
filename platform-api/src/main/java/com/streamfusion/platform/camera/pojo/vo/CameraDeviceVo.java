package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

@Schema(description = "本地设备观测档案，仅超级管理员可查询")
public record CameraDeviceVo(
        @Schema(description = "本地设备稳定ID") String deviceId,
        @Schema(description = "所属接入来源ID") String sourceId,
        @Schema(description = "来源内稳定设备键") String externalDeviceKey,
        @Schema(description = "上游原始设备标识，不含秘密", nullable = true) String externalDeviceRef,
        @Schema(description = "设备类型：IPC/NVR/DVR/ENCODER/UNKNOWN") String deviceType,
        @Schema(description = "上游设备名称", nullable = true) String sourceName,
        @Schema(description = "当前显示名称") String name,
        @Schema(description = "本地显示名称覆盖", nullable = true) String localName,
        @Schema(description = "本地设备备注", nullable = true) String remark,
        @Schema(description = "可靠观测制造商", nullable = true) String manufacturer,
        @Schema(description = "可靠观测型号", nullable = true) String model,
        @Schema(description = "设备报告的序列号，不作跨来源唯一键", nullable = true) String serialNumber,
        @Schema(description = "可靠观测固件版本", nullable = true) String firmwareVersion,
        @Schema(description = "最近可靠观测时间，UTC；不表示当前在线", nullable = true) Instant infoObservedAt,
        @Schema(description = "身份配置编辑版本，纯观测不递增") String version,
        @Schema(description = "本地档案创建时间，UTC") Instant createdAt,
        @Schema(description = "身份配置更新时间，UTC") Instant updatedAt) {}
