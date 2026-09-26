package com.streamfusion.platform.role.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "角色详情")
public record RoleDetailVo(
        @Schema(description = "角色ID") String id,
        @Schema(description = "角色编码") String code,
        @Schema(description = "角色名称") String name,
        @Schema(description = "角色说明") String description,
        @Schema(description = "角色状态") String status,
        @Schema(description = "编辑版本") String version) {}
