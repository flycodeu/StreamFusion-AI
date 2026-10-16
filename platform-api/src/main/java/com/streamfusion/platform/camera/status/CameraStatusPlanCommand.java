package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

public record CameraStatusPlanCommand(
        @Schema(description = "客户端请求编号，重试时沿用同一编号") String clientRequestId,
        @Schema(description = "设备分组键列表：d加设备ID或c加独立通道ID，最多2000项") List<String> groupKeys,
        @Schema(description = "每台设备的检查间隔，5至1440分钟") Integer intervalMinutes,
        @Schema(description = "任务执行时长，1至168小时，不能短于检查间隔") Integer durationHours,
        @Schema(description = "CONTROL连接检查或MEDIA视频收帧") CameraStatusLayer layer,
        @Schema(description = "POLLING逐个执行或PARALLEL有界并行") String dispatchMode,
        @Schema(description = "任务最大并发，1至4；MEDIA固定为1") Integer parallelism,
        @Schema(description = "按视频分组创建时的分组ID，包含子分组") List<String> groupIds,
        @Schema(description = "复用本人已有任务的固定通道范围；不能与groupIds同时提供") String planId) {
    public CameraStatusPlanCommand(
            String clientRequestId,
            List<String> groupKeys,
            Integer intervalMinutes,
            Integer durationHours) {
        this(
                clientRequestId,
                groupKeys,
                intervalMinutes,
                durationHours,
                CameraStatusLayer.CONTROL,
                "POLLING",
                1,
                null,
                null);
    }

    public CameraStatusLayer effectiveLayer() {
        return layer == null ? CameraStatusLayer.CONTROL : layer;
    }

    public String effectiveMode() {
        return dispatchMode == null ? "POLLING" : dispatchMode;
    }

    public int effectiveParallelism() {
        return parallelism == null ? 1 : parallelism;
    }
}
