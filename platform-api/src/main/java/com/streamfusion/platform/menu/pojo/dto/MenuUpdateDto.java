package com.streamfusion.platform.menu.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

/** PUT replaces all editable fields and requires the version last read by the client. */
@Getter
@Setter
@Schema(description = "菜单完整编辑请求")
public class MenuUpdateDto extends MenuWriteDto {
    @Schema(description = "菜单编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;
}
