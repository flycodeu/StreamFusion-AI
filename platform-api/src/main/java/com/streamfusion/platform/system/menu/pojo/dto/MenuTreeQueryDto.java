package com.streamfusion.platform.system.menu.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

/** Optional management-tree filters. Matching descendants retain their ancestors. */
@Getter
@Setter
@Schema(description = "菜单树查询")
public class MenuTreeQueryDto {
    @Schema(description = "菜单名称")
    private String name;

    @Schema(description = "菜单类型")
    private String type;

    @Schema(description = "启用状态")
    private Boolean enabled;

    @Schema(description = "按层读取；true仅返回parentId的直接下级，未指定parentId时返回根节点；省略保持整树查询")
    private Boolean lazy;

    @Schema(description = "按层查询的父节点ID，仅lazy=true时可使用")
    private String parentId;
}
