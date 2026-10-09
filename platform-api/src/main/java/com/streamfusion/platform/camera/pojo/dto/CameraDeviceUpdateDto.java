package com.streamfusion.platform.camera.pojo.dto;

import com.fasterxml.jackson.annotation.JsonIgnore;
import com.fasterxml.jackson.annotation.JsonSetter;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "设备本地资料局部更新，不修改接入来源或上游观测资料")
public class CameraDeviceUpdateDto {
    @Schema(description = "设备本地资料编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;

    @Schema(description = "本地显示名称；显式null恢复来源名称", maxLength = 128, nullable = true)
    private String localName;

    @Schema(description = "本地备注；显式null清空", maxLength = 500, nullable = true)
    private String remark;

    @JsonIgnore private boolean localNameProvided;
    @JsonIgnore private boolean remarkProvided;

    @JsonSetter("localName")
    public void setLocalName(String value) {
        localName = value;
        localNameProvided = true;
    }

    @JsonSetter("remark")
    public void setRemark(String value) {
        remark = value;
        remarkProvided = true;
    }
}
