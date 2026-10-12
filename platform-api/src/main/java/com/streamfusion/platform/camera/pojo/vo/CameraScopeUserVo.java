package com.streamfusion.platform.camera.pojo.vo;

import com.streamfusion.platform.user.pojo.vo.DepartmentVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "相机授权目标账户及所属部门")
public record CameraScopeUserVo(
        @Schema(description = "账户ID") String userId,
        @Schema(description = "账号") String username,
        @Schema(description = "昵称") String nickname,
        @Schema(description = "账号是否未被手动停用") boolean enabled,
        @Schema(description = "是否超级管理员") boolean isSuperAdmin,
        @Schema(description = "当前关联的部门，没有关联时为空列表") List<DepartmentVo> departments) {}
