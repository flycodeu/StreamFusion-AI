package com.streamfusion.platform.role.pojo.vo;

import com.streamfusion.platform.access.pojo.vo.RoleVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "用户角色分配")
public record UserRolesVo(
        @Schema(description = "用户ID") String userId,
        @Schema(description = "用户编辑版本") String version,
        @Schema(description = "已分配角色") List<RoleVo> roles) {}
