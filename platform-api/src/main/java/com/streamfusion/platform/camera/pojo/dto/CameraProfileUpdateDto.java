package com.streamfusion.platform.camera.pojo.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonSetter;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "码流档案局部更新，不修改远程设备编码参数")
public class CameraProfileUpdateDto {
    @Schema(description = "码流编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;

    @Schema(description = "本地码流标签", maxLength = 64)
    private String label;

    @Schema(
            description = "人工选择的码流用途",
            allowableValues = {"MAIN", "SUB", "THIRD", "CUSTOM", "UNKNOWN"})
    private String usageHint;

    @Schema(description = "是否允许新使用该码流")
    private Boolean enabled;

    @Schema(
            description = "定位类型，既有类型不可切换",
            allowableValues = {"RTSP"})
    private String locatorKind;

    @Schema(description = "手工RTSP定位更新；发现型码流身份不可由此修改")
    private CameraLocatorDto locator;

    @Schema(description = "禁用当前默认码流并同时替换默认值时必传的相机版本")
    private String cameraVersion;

    @Schema(description = "禁用当前默认码流时必须显式提交；另一合法码流ID或null清空", nullable = true)
    private String replacementDefaultProfileId;

    @JsonIgnore private boolean labelProvided;
    @JsonIgnore private boolean usageHintProvided;
    @JsonIgnore private boolean enabledProvided;
    @JsonIgnore private boolean locatorKindProvided;
    @JsonIgnore private boolean locatorProvided;
    @JsonIgnore private boolean replacementDefaultProfileIdProvided;

    @JsonSetter("label")
    public void setLabel(String value) {
        label = value;
        labelProvided = true;
    }

    @JsonSetter("usageHint")
    public void setUsageHint(String value) {
        usageHint = value;
        usageHintProvided = true;
    }

    @JsonSetter("enabled")
    public void setEnabled(Boolean value) {
        enabled = value;
        enabledProvided = true;
    }

    @JsonSetter("locatorKind")
    public void setLocatorKind(String value) {
        locatorKind = value;
        locatorKindProvided = true;
    }

    @JsonSetter("locator")
    public void setLocator(CameraLocatorDto value) {
        locator = value;
        locatorProvided = true;
    }

    @JsonSetter("replacementDefaultProfileId")
    public void setReplacementDefaultProfileId(String value) {
        replacementDefaultProfileId = value;
        replacementDefaultProfileIdProvided = true;
    }
}
