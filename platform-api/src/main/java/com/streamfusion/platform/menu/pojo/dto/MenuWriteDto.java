package com.streamfusion.platform.menu.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

/** Full editable menu input. A page's frontend component is resolved later by its key. */
@Getter
@Setter
@Schema(description = "菜单新增及完整编辑字段")
public class MenuWriteDto {
    @Schema(description = "上级菜单ID")
    private String parentId;

    @Schema(description = "菜单名称", requiredMode = Schema.RequiredMode.REQUIRED)
    private String name;

    @Schema(description = "菜单类型", requiredMode = Schema.RequiredMode.REQUIRED)
    private String type;

    @Schema(description = "页面路由名称")
    private String routeName;

    @Schema(description = "页面路由路径")
    private String path;

    @Schema(description = "前端组件键")
    private String componentKey;

    @Schema(description = "后端模块键")
    private String moduleKey;

    @Schema(description = "图标键")
    private String icon;

    @Schema(description = "排序值", requiredMode = Schema.RequiredMode.REQUIRED)
    private Integer sortOrder;

    @Schema(description = "导航显示状态", requiredMode = Schema.RequiredMode.REQUIRED)
    private Boolean visible;

    @Schema(description = "启用状态", requiredMode = Schema.RequiredMode.REQUIRED)
    private Boolean enabled;
}
