package com.streamfusion.platform.menu.pojo.vo;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

/** Compact management tree node. Only pages have page; only directories have children. */
@JsonInclude(JsonInclude.Include.NON_NULL)
@Schema(description = "菜单管理树节点")
public record MenuNodeVo(
        @Schema(description = "菜单ID") String id,
        @Schema(description = "上级菜单ID") String parentId,
        @Schema(description = "菜单名称") String name,
        @Schema(description = "菜单类型") String type,
        @Schema(description = "图标键") String icon,
        @Schema(description = "排序值") int sortOrder,
        @Schema(description = "导航显示状态") boolean visible,
        @Schema(description = "启用状态") boolean enabled,
        @Schema(description = "编辑版本") String version,
        @Schema(description = "页面路由配置") MenuPageVo page,
        @Schema(description = "下级菜单") List<MenuNodeVo> children) {}
