package com.streamfusion.platform.user.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

/** User management may replace optional department assignments with the same edit version. */
@Getter
@Setter
@Schema(description = "管理端修改用户资料与所属部门")
public class UserManagementUpdateDto extends UserProfileUpdateDto {
    @Schema(description = "所属部门ID，最多20个；省略保留现有关联，空数组清空部门")
    private List<String> departmentIds;
}
