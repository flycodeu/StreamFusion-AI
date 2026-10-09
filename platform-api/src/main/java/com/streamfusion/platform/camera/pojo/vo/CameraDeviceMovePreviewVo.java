package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "整台设备全部通道的移组确认")
public record CameraDeviceMovePreviewVo(
        @Schema(description = "设备集合标识，与设备列表groupKey一致") String groupKey,
        @Schema(description = "全部通道当前归属的分组及数量") List<Placement> placements,
        @Schema(description = "整机移组的授权影响及确认凭据") CameraImpactVo impact) {
    @Schema(description = "设备内通道的当前分组分布")
    public record Placement(
            @Schema(description = "分组ID，待归档时为空") String groupId,
            @Schema(description = "当前分组完整路径或待归档") String groupPath,
            @Schema(description = "此分组内的通道数") long channelCount) {}
}
