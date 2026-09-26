package com.streamfusion.platform.access.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 用户角色批量查询的内部结果。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "用户角色批量查询结果", hidden = true)
public class UserRoleAssignmentDto {
    /** 所属用户主键，仅用于后端结果分组。 */
    @Schema(description = "所属用户主键")
    private Long userId;

    /** 角色主键，以十进制字符串传输。 */
    @Schema(description = "角色ID")
    private String id;

    /** 角色编码。 */
    @Schema(description = "角色编码")
    private String code;

    /** 角色显示名称。 */
    @Schema(description = "角色显示名称")
    private String name;
}
