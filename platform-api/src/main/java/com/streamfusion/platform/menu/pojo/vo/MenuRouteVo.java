package com.streamfusion.platform.menu.pojo.vo;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

/** Current user's published route tree, including each page's backend module binding. */
@JsonInclude(JsonInclude.Include.NON_NULL)
@Schema(description = "当前用户页面路由")
public record MenuRouteVo(
        @Schema(description = "菜单ID") String id,
        @Schema(description = "菜单名称") String name,
        @Schema(description = "菜单类型") String type,
        @Schema(description = "图标键") String icon,
        @Schema(description = "导航显示状态") boolean visible,
        @Schema(description = "路由名称") String routeName,
        @Schema(description = "路由路径") String path,
        @Schema(description = "页面文件路径，相对views且不带.vue") String componentKey,
        @Schema(description = "后端模块键，仅页面存在") String moduleKey,
        @Schema(description = "下级路由") List<MenuRouteVo> children) {}
