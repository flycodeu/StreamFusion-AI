package com.streamfusion.platform.camera.pojo.dto;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "当前账户范围内的相机分页筛选")
public class CameraQueryDto extends PageQueryDto {
    @Schema(description = "相机名称关键词，通配符按字面查询", maxLength = 128, nullable = true)
    private String name;

    @Schema(description = "视频分组ID", nullable = true)
    private String groupId;

    @Schema(description = "分组筛选是否包含后代分组", defaultValue = "false")
    private boolean includeDescendants;

    @Schema(description = "接入来源ID；此筛选不扩大账户可见范围", nullable = true)
    private String sourceId;

    @Schema(
            description = "接入适配方式",
            allowableValues = {"RTSP", "ONVIF", "HIKVISION", "DAHUA"},
            nullable = true)
    private String adapterType;

    @Schema(
            description = "相机生命周期；待归档仅超管可见",
            allowableValues = {"PENDING_ASSIGNMENT", "ENABLED", "DISABLED"},
            nullable = true)
    private String lifecycle;
}
