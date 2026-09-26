package com.streamfusion.platform.role.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "修改角色")
public class RoleUpdateDto {
    @Schema(description = "角色名称", requiredMode = Schema.RequiredMode.REQUIRED)
    private String name;

    @Schema(description = "角色说明")
    private String description;

    @Schema(description = "角色编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;
}
