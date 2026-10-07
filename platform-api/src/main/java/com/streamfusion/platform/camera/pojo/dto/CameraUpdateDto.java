package com.streamfusion.platform.camera.pojo.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonSetter;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

/** Presence is part of this partial update: null clears only explicitly nullable values. */
@Getter
@Setter
@Schema(description = "相机局部更新；未提交字段保留，允许为空的字段可显式清空")
public class CameraUpdateDto {
    @Schema(description = "相机编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;

    @Schema(description = "本地相机名称", maxLength = 128)
    private String name;

    @Schema(description = "本地备注；显式null清空", maxLength = 500, nullable = true)
    private String remark;

    @Schema(description = "本通道启用且定位有效的默认码流ID；显式null清空", nullable = true)
    private String defaultPreviewProfileId;

    @Schema(description = "目标视频分组ID；仅超管，归档或移组需要影响确认")
    private String groupId;

    @Schema(
            description = "目标生命周期；仅超管，状态变化需要影响确认",
            allowableValues = {"ENABLED", "DISABLED"})
    private String lifecycle;

    @Schema(description = "归档、移组或启停预览返回的短期影响确认凭据")
    private String confirmation;

    @JsonIgnore private boolean nameProvided;
    @JsonIgnore private boolean remarkProvided;
    @JsonIgnore private boolean defaultPreviewProfileIdProvided;
    @JsonIgnore private boolean groupIdProvided;
    @JsonIgnore private boolean lifecycleProvided;
    @JsonIgnore private boolean confirmationProvided;

    @JsonSetter("name")
    public void setName(String value) {
        name = value;
        nameProvided = true;
    }

    @JsonSetter("remark")
    public void setRemark(String value) {
        remark = value;
        remarkProvided = true;
    }

    @JsonSetter("defaultPreviewProfileId")
    public void setDefaultPreviewProfileId(String value) {
        defaultPreviewProfileId = value;
        defaultPreviewProfileIdProvided = true;
    }

    @JsonSetter("groupId")
    public void setGroupId(String value) {
        groupId = value;
        groupIdProvided = true;
    }

    @JsonSetter("lifecycle")
    public void setLifecycle(String value) {
        lifecycle = value;
        lifecycleProvided = true;
    }

    @JsonSetter("confirmation")
    public void setConfirmation(String value) {
        confirmation = value;
        confirmationProvided = true;
    }
}
