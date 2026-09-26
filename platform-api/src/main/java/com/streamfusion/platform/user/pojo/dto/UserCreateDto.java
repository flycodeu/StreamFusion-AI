package com.streamfusion.platform.user.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 创建普通用户请求。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "创建普通用户请求")
public class UserCreateDto {
    /** 登录账号，4至32位英文字母和数字。 */
    @Schema(
            description = "登录账号",
            minLength = 4,
            maxLength = 32,
            pattern = "^[A-Za-z0-9]{4,32}$",
            nullable = false,
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String username;

    /** 昵称。 */
    @Schema(description = "昵称", nullable = true, requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private String nickname;

    /** 头像标识。 */
    @Schema(description = "头像标识", nullable = true, requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private String avatarKey;

    /** 联系电话。 */
    @Schema(description = "联系电话", nullable = true, requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private String phone;

    /** 联系邮箱。 */
    @Schema(description = "联系邮箱", nullable = true, requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private String email;

    /** 性别：0未设置，1男，2女。 */
    @Schema(
            description = "性别",
            allowableValues = {"0", "1", "2"},
            defaultValue = "0",
            nullable = true,
            requiredMode = Schema.RequiredMode.NOT_REQUIRED)
    private Integer gender;

    @Schema(description = "所属部门ID，最多20个；省略或空数组表示不分配部门")
    private List<String> departmentIds;
}
