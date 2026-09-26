package com.streamfusion.platform.role.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "角色选择项")
public record RoleOptionVo(
        @Schema(description = "角色ID") String id,
        @Schema(description = "角色编码") String code,
        @Schema(description = "角色名称") String name) {}
