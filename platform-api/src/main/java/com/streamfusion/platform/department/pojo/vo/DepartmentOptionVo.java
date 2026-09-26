package com.streamfusion.platform.department.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "部门选择项")
public record DepartmentOptionVo(
        @Schema(description = "部门ID") String id,
        @Schema(description = "上级部门ID") String parentId,
        @Schema(description = "部门名称") String name) {}
