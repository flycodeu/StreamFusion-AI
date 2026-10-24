package com.streamfusion.platform.system.user.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

/** User management saves profile, optional departments and optional roles in one transaction. */
@Getter
@Setter
@Schema(description = "管理端修改用户资料、所属部门与角色")
public class UserManagementUpdateDto extends UserProfileUpdateDto {
    @Schema(description = "所属部门ID，最多20个；省略保留现有关联，空数组清空部门")
    private List<String> departmentIds;

    @Schema(description = "角色ID；省略保留现有关联，空数组清空可移除角色")
    private List<String> roleIds;
}
