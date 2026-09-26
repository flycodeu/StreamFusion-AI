package com.streamfusion.platform.user.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 只读部门摘要。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "只读部门摘要")
public class DepartmentVo {
    /** 部门ID，十进制字符串。 */
    @Schema(description = "部门ID")
    private String id;

    /** 部门名称。 */
    @Schema(description = "部门名称")
    private String name;
}
