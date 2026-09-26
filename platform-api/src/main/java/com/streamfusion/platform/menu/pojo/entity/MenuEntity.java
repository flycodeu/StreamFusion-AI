package com.streamfusion.platform.menu.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** Internal persistence model for a directory or page menu. */
@Getter
@Setter
@TableName("sys_menu")
@Schema(description = "菜单数据", hidden = true)
public class MenuEntity {
    @TableId(value = "id", type = IdType.ASSIGN_ID)
    @Schema(description = "菜单ID")
    private Long id;

    @Schema(description = "上级菜单ID")
    private Long parentId;

    @Schema(description = "菜单名称")
    private String name;

    @Schema(description = "菜单类型")
    private String type;

    @Schema(description = "路由名称")
    private String routeName;

    @Schema(description = "路由路径")
    private String path;

    @Schema(description = "组件键")
    private String componentKey;

    @Schema(description = "模块键")
    private String moduleKey;

    @Schema(description = "图标键")
    private String icon;

    @Schema(description = "排序值")
    private Integer sortOrder;

    @Schema(description = "导航显示状态")
    private Boolean visible;

    @Schema(description = "启用状态")
    private Boolean enabled;

    @Schema(description = "编辑版本")
    private Long version;

    @Schema(description = "创建时间")
    private LocalDateTime createdAt;

    @Schema(description = "更新时间")
    private LocalDateTime updatedAt;

    @Schema(description = "创建人ID")
    private Long createdBy;

    @Schema(description = "更新人ID")
    private Long updatedBy;
}
