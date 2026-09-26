package com.streamfusion.platform.department.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "部门树节点")
public record DepartmentNodeVo(
        @Schema(description = "部门ID") String id,
        @Schema(description = "上级部门ID") String parentId,
        @Schema(description = "部门名称") String name,
        @Schema(description = "排序值") int sortOrder,
        @Schema(description = "直属用户数") long memberCount,
        @Schema(description = "编辑版本") String version,
        @Schema(description = "下级部门") List<DepartmentNodeVo> children) {}
