package com.streamfusion.platform.department.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "设置用户部门")
public class UserDepartmentsUpdateDto {
    @Schema(description = "用户编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;

    @Schema(description = "部门ID列表", requiredMode = Schema.RequiredMode.REQUIRED)
    private List<String> departmentIds;
}
