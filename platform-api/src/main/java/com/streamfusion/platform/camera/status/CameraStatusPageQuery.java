package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "当前相机范围内按状态层筛选")
public class CameraStatusPageQuery extends PageQueryDto {
    @Schema(description = "汇总与事实筛选层：CONTROL/MEDIA", requiredMode = Schema.RequiredMode.REQUIRED)
    private CameraStatusLayer layer;

    @Schema(description = "设备或通道名称关键词，通配符按字面匹配", nullable = true, maxLength = 128)
    private String name;

    @Schema(
            description = "匹配所选层低负载代表码流最近事实。设备入口先选授权代表通道，再筛选事实；NEVER含无代表流/无事实",
            allowableValues = {"NEVER", "ACCESSIBLE", "FAILED", "UNSUPPORTED"},
            nullable = true)
    private String observation;
}
