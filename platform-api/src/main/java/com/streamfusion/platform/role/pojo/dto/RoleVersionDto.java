package com.streamfusion.platform.role.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "角色状态变更")
public class RoleVersionDto {
    @Schema(description = "角色编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;
}
