package com.streamfusion.platform.menu.pojo.dto;

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
}
