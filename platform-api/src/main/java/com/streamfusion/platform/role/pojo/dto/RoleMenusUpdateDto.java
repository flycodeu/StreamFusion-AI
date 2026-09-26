package com.streamfusion.platform.role.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "设置角色菜单")
public class RoleMenusUpdateDto {
    @Schema(description = "菜单ID列表", requiredMode = Schema.RequiredMode.REQUIRED)
    private List<String> menuIds;

    @Schema(description = "角色编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;
}
