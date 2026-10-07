package com.streamfusion.platform.camera.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "为手工相机增加一份RTSP码流")
public class CameraProfileCreateDto {
    @Schema(description = "读取相机时的编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String cameraVersion;

    @Schema(description = "创建幂等键：13位Unix毫秒时间戳-UUIDv4", requiredMode = Schema.RequiredMode.REQUIRED)
    private String clientRequestId;

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
            description = "定位方式；手工新增仅支持RTSP",
            allowableValues = {"RTSP"},
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String locatorKind;

    @Schema(description = "类型化RTSP定位配置，秘密只写不回显", requiredMode = Schema.RequiredMode.REQUIRED)
    private CameraLocatorDto locator;
}
