package com.streamfusion.platform.camera.pojo.dto;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
public class CameraSourceQueryDto extends PageQueryDto {
    @Schema(description = "接入来源名称")
    private String name;

    @Schema(description = "接入方式，本阶段固定RTSP")
    private String adapterType;

    @Schema(description = "是否允许新使用")
    private Boolean enabled;
}
