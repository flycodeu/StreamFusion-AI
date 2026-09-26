package com.streamfusion.platform.role.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "角色菜单分配")
public record RoleMenusVo(
        @Schema(description = "角色ID") String roleId,
        @Schema(description = "角色编辑版本") String version,
        @Schema(description = "已选页面ID") List<String> selectedPageIds,
        @Schema(description = "可分配菜单树") List<RoleMenuNodeVo> tree) {}
