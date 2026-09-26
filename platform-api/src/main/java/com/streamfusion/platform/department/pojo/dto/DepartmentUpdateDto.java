package com.streamfusion.platform.department.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "修改部门")
public class DepartmentUpdateDto extends DepartmentWriteDto {
    @Schema(description = "编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;
}
