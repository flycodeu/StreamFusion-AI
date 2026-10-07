package com.streamfusion.platform.camera.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "新建相机时提交的单份手工码流")
public class CameraInitialProfileDto {
    @Schema(
            description = "本次创建请求内唯一的码流引用键",
            maxLength = 64,
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String clientKey;

    @Schema(description = "本地码流显示标签", maxLength = 64, requiredMode = Schema.RequiredMode.REQUIRED)
    private String label;

    @Schema(
            description = "码流用途，不代表码率大小",
            allowableValues = {"MAIN", "SUB", "THIRD", "CUSTOM", "UNKNOWN"},
            defaultValue = "UNKNOWN")
    private String usageHint = "UNKNOWN";

    @Schema(description = "是否允许新使用该码流", defaultValue = "true")
    private Boolean enabled = true;

    @Schema(
            description = "定位方式；本次手工登记仅支持RTSP",
            allowableValues = {"RTSP"},
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String locatorKind;

    @Schema(description = "类型化RTSP定位配置，路径和查询参数只写不回显", requiredMode = Schema.RequiredMode.REQUIRED)
    private CameraLocatorDto locator;
}
