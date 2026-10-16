package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;

@Schema(description = "单层观察事实，不证明设备整体在线、解码或画面正常")
public record CameraStatusObservationVo(
        @Schema(description = "事实值：ACCESSIBLE/FAILED/UNSUPPORTED") String value,
        @Schema(description = "受控事实原因码") String reasonCode,
        @Schema(description = "事实观测时间，UTC，不随GET刷新") Instant observedAt,
        @Schema(description = "采集器读取事实时间，UTC，不是GET时间") Instant readAt,
        @Schema(description = "历史兼容字段，UTC；不再用于结果失效判断，最近结果持续保留") Instant validUntil,
        @Schema(description = "产生该事实的检查ID") String checkId,
        @Schema(description = "MEDIA接收计数证据，CONTROL为空", nullable = true)
                CameraMediaEvidence mediaEvidence) {
    public static CameraStatusObservationVo from(CameraStatusSnapshotEntity snapshot) {
        return new CameraStatusObservationVo(
                snapshot.getValue(),
                snapshot.getReasonCode(),
                instant(snapshot.getObservedAt()),
                instant(snapshot.getReadAt()),
                instant(snapshot.getValidUntil()),
                snapshot.getCheckId().toString(),
                evidence(snapshot));
    }

    public static CameraStatusObservationVo from(CameraStatusCheckEntity check) {
        return new CameraStatusObservationVo(
                check.getResultValue(),
                check.getReasonCode(),
                instant(check.getObservedAt()),
                instant(check.getReadAt()),
                instant(check.getValidUntil()),
                check.getId().toString(),
                check.getMediaWindowStartedAt() == null
                        ? null
                        : new CameraMediaEvidence(
                                instant(check.getMediaWindowStartedAt()),
                                instant(check.getMediaWindowEndedAt()),
                                check.getMediaFirstFrames(),
                                check.getMediaLastFrames(),
                                check.getMediaSampleCount(),
                                check.getMediaMaxGapMs()));
    }

    private static CameraMediaEvidence evidence(CameraStatusSnapshotEntity snapshot) {
        return snapshot.getMediaWindowStartedAt() == null
                ? null
                : new CameraMediaEvidence(
                        instant(snapshot.getMediaWindowStartedAt()),
                        instant(snapshot.getMediaWindowEndedAt()),
                        snapshot.getMediaFirstFrames(),
                        snapshot.getMediaLastFrames(),
                        snapshot.getMediaSampleCount(),
                        snapshot.getMediaMaxGapMs());
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZoneId.of("Asia/Shanghai")).toInstant();
    }
}
