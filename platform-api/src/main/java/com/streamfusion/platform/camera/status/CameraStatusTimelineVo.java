package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;

@Schema(description = "同一配置的本层采样时间，不是设备真实上下线或连续离线时长")
public record CameraStatusTimelineVo(
        @Schema(description = "本轮相同结果首次检出时间；旧数据从已知观察时间起算") Instant stateSince,
        @Schema(description = "本层最近一次检查成功的观察时间，未知为空") Instant lastAccessibleAt,
        @Schema(description = "本层最近一次检查失败的观察时间，未知为空") Instant lastFailedAt) {
    public static CameraStatusTimelineVo from(CameraStatusSnapshotEntity fact) {
        if (fact == null) return null;
        return new CameraStatusTimelineVo(
                instant(fact.getStateSince() == null ? fact.getObservedAt() : fact.getStateSince()),
                instant(
                        fact.getLastAccessibleAt() == null && "ACCESSIBLE".equals(fact.getValue())
                                ? fact.getObservedAt()
                                : fact.getLastAccessibleAt()),
                instant(
                        fact.getLastFailedAt() == null && "FAILED".equals(fact.getValue())
                                ? fact.getObservedAt()
                                : fact.getLastFailedAt()));
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZoneId.of("Asia/Shanghai")).toInstant();
    }
}
