package com.streamfusion.platform.menu.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

/** Page-only route fields; directories do not serialize this object. */
@Schema(description = "页面路由配置")
public record MenuPageVo(
        @Schema(description = "路由名称") String routeName,
        @Schema(description = "路由路径") String path,
        @Schema(description = "页面文件路径，相对views且不带.vue") String componentKey,
        @Schema(description = "后端模块键") String moduleKey) {}
