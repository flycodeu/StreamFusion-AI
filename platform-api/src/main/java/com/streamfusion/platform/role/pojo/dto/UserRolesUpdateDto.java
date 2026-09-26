package com.streamfusion.platform.role.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "设置用户角色")
public class UserRolesUpdateDto {
    @Schema(description = "角色ID列表", requiredMode = Schema.RequiredMode.REQUIRED)
    private List<String> roleIds;

    @Schema(description = "用户编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;
}
