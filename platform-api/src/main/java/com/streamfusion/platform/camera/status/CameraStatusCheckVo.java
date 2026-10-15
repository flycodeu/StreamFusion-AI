package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;

@Schema(description = "单次手动检查执行回执，执行状态不等于相机事实")
public record CameraStatusCheckVo(
        @Schema(description = "检查ID") String checkId,
        @Schema(description = "所属相机通道ID") String cameraId,
        @Schema(description = "本次选定的码流ID") String streamProfileId,
        @Schema(description = "检查类型，本期固定RTSP_DESCRIBE") String probeKind,
        @Schema(description = "执行状态：QUEUED/RUNNING/COMPLETED/SKIPPED/CANCELLED/INTERRUPTED")
                String executionState,
        @Schema(description = "服务器接受时间，UTC") Instant requestedAt,
        @Schema(description = "领取执行时间，UTC，未执行为空", nullable = true) Instant startedAt,
        @Schema(description = "执行结束时间，UTC，未结束为空", nullable = true) Instant finishedAt,
        @Schema(description = "执行资格截止，UTC，独立于探针预算") Instant deadlineAt,
        @Schema(description = "本次观察事实，未获得事实为空", nullable = true)
                CameraStatusObservationVo observation,
        @Schema(description = "受控执行或事实原因码，无原因为空", nullable = true) String reasonCode,
        @Schema(description = "探针耗时毫秒，未执行可空", nullable = true) Long durationMs,
        @Schema(description = "原检查请求的日志链路标识") String traceId,
        @Schema(description = "是否通过写回围栏并更新当前快照") boolean snapshotApplied) {
    public static CameraStatusCheckVo from(CameraStatusCheckEntity check) {
        return new CameraStatusCheckVo(
                check.getId().toString(),
                check.getCameraId().toString(),
                check.getProfileId().toString(),
                check.getProbeKind(),
                check.getExecutionState(),
                instant(check.getRequestedAt()),
                instant(check.getStartedAt()),
                instant(check.getFinishedAt()),
                instant(check.getDeadlineAt()),
                check.getResultValue() == null
                        ? null
                        : new CameraStatusObservationVo(
                                check.getResultValue(),
                                check.getReasonCode(),
                                instant(check.getObservedAt()),
                                instant(check.getReadAt()),
                                instant(check.getValidUntil()),
                                check.getId().toString()),
                check.getReasonCode(),
                check.getDurationMs(),
                check.getTraceId(),
                Boolean.TRUE.equals(check.getSnapshotApplied()));
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZoneId.of("Asia/Shanghai")).toInstant();
    }
}
