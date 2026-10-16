package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

public record CameraStatusPlanVo(
        @Schema(description = "巡检计划ID") String planId,
        @Schema(description = "计划期望状态：ACTIVE/STOPPED/EXPIRED") String state,
        @Schema(description = "各目标检测间隔，分钟") int intervalMinutes,
        @Schema(description = "创建时固定的检测目标数量；新计划每台设备选择一路代表流") int targetCount,
        @Schema(description = "首次时间槽起点，UTC") Instant startAt,
        @Schema(description = "有限期执行截止时间，UTC") Instant endAt,
        @Schema(description = "停止操作使用的并发版本") String version,
        @Schema(description = "当前已阻止继续检测的目标数") int blockedCount,
        @Schema(description = "未阻止目标的下一时间槽，UTC，可空") Instant nextAt,
        @Schema(description = "最近派发或执行未完成的原因码，可空") String lastReasonCode,
        @Schema(description = "每个目标最近一次调度的回执汇总，不是固定整点轮次；创建/停止响应可空") Progress progress,
        @Schema(description = "响应服务器时间，UTC") Instant serverNow,
        @Schema(description = "直连检查层：CONTROL或MEDIA；平台目标执行UPSTREAM") CameraStatusLayer layer,
        @Schema(description = "调度方式：POLLING逐个轮询、PARALLEL有界并行") String dispatchMode,
        @Schema(description = "单任务最大进行中检查数，1至4；含视频检查时为1") int parallelism,
        @Schema(description = "连接通过后检查直连视频；平台目标仅查询在线状态") boolean checkVideo) {
    public record Progress(int success, int failed, int pending, int skipped, int unknown) {}
}
