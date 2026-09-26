package com.streamfusion.platform.role.pojo.dto;

import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "角色分页查询")
public class RoleQueryDto extends PageQueryDto {
    @Schema(description = "角色编码或名称")
    private String keyword;

    @Schema(description = "角色状态")
    private String status;
}
