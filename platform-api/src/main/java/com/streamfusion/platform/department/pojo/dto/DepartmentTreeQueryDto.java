package com.streamfusion.platform.department.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "部门树查询")
public class DepartmentTreeQueryDto {
    @Schema(description = "部门名称")
    private String name;
}
