package com.streamfusion.platform.role.pojo.vo;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@JsonInclude(JsonInclude.Include.NON_NULL)
@Schema(description = "角色菜单选择节点")
public record RoleMenuNodeVo(
        @Schema(description = "菜单ID") String id,
        @Schema(description = "菜单名称") String name,
        @Schema(description = "菜单类型") String type,
        @Schema(description = "启用状态") boolean enabled,
        @Schema(description = "下级菜单") List<RoleMenuNodeVo> children) {}
