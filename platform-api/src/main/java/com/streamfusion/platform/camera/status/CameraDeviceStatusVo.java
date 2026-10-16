package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

/** CONTROL evidence only; a reachable endpoint does not prove media decoding. */
public record CameraDeviceStatusVo(
        @Schema(description = "单路代表流连接状态，不等于整机在线或其他通道媒体健康") String state,
        @Schema(description = "最近获得连接事实的时间，UTC，可空") Instant checkedAt,
        @Schema(description = "历史兼容期限，UTC，可空；不再据此使最近结果失效") Instant validUntil,
        @Schema(description = "代表流具有有效可达事实时为1，否则0") int reachableCount,
        @Schema(description = "代表流具有有效失败事实时为1，否则0") int failedCount,
        @Schema(description = "代表流没有有效事实时为1，无启用通道时为0") int unknownCount) {}
