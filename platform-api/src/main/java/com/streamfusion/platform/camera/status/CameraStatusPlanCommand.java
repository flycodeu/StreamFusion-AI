package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

public record CameraStatusPlanCommand(
        @Schema(description = "客户端请求编号，重试时沿用同一编号") String clientRequestId,
        @Schema(description = "设备分组键列表：d加设备ID或c加独立通道ID，最多2000项") List<String> groupKeys,
        @Schema(description = "每台设备的检查间隔，5至1440分钟") Integer intervalMinutes,
        @Schema(description = "任务执行时长，1至168小时，不能短于检查间隔") Integer durationHours) {}
