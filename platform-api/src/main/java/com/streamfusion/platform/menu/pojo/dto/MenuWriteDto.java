package com.streamfusion.platform.menu.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

/** Full editable menu input; page and module mappings default to path and routeName. */
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

    @Schema(
            description = "页面唯一键；未指定moduleKey时也用作后端模块键",
            maxLength = 64,
            pattern = "[A-Za-z][A-Za-z0-9_:-]{0,63}")
    private String routeName;

    @Schema(description = "页面路由路径，以/开头，默认同时定位views中的同名.vue页面", maxLength = 200)
    private String path;

    @Schema(description = "可选页面文件路径，相对views且不带.vue；以/开头，缺省使用path", maxLength = 64)
    private String componentKey;

    @Schema(description = "可选后端模块键，对应已注册接口的@ModuleAccess值；缺省使用routeName，创建后不可更改", maxLength = 64)
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
