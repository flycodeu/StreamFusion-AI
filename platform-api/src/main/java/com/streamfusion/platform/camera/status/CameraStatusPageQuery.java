package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "当前相机范围内的控制层历史状态分页筛选")
public class CameraStatusPageQuery extends PageQueryDto {
    @Schema(description = "设备或通道名称关键词，通配符按字面匹配", nullable = true, maxLength = 128)
    private String name;

    @Schema(
            description = "匹配启用代表码流最近一次CONTROL结果（含历史），不是实时状态；默认流优先，其次MAIN、最小ID。NEVER含无代表流/无事实",
            allowableValues = {"NEVER", "ACCESSIBLE", "FAILED", "UNSUPPORTED"},
            nullable = true)
    private String observation;
}
