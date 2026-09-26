package com.streamfusion.platform.access.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 当前用户可见的角色简要信息。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "角色简要信息")
public class RoleVo {
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
