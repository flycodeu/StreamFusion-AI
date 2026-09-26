package com.streamfusion.platform.department.pojo.vo;

import com.streamfusion.platform.user.pojo.vo.DepartmentVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "用户部门归属")
public record UserDepartmentsVo(
        @Schema(description = "用户ID") String userId,
        @Schema(description = "用户编辑版本") String version,
        @Schema(description = "所属部门") List<DepartmentVo> departments) {}
