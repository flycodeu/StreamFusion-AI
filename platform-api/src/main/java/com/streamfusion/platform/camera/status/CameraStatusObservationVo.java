package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;

@Schema(description = "RTSP控制路径观察事实，不证明媒体可播放或设备整体在线")
public record CameraStatusObservationVo(
        @Schema(description = "事实值：ACCESSIBLE/FAILED/UNSUPPORTED") String value,
        @Schema(description = "受控事实原因码") String reasonCode,
        @Schema(description = "事实观测时间，UTC，不随GET刷新") Instant observedAt,
        @Schema(description = "采集器读取事实时间，UTC，不是GET时间") Instant readAt,
        @Schema(description = "事实有效截止，UTC，过期不代表离线") Instant validUntil,
        @Schema(description = "产生该事实的检查ID") String checkId) {
    public static CameraStatusObservationVo from(CameraStatusSnapshotEntity snapshot) {
        return new CameraStatusObservationVo(
                snapshot.getValue(),
                snapshot.getReasonCode(),
                instant(snapshot.getObservedAt()),
                instant(snapshot.getReadAt()),
                instant(snapshot.getValidUntil()),
                snapshot.getCheckId().toString());
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZoneId.of("Asia/Shanghai")).toInstant();
    }
}
