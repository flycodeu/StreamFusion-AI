package com.streamfusion.platform.user.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 用户资料完整替换请求，可选字段缺失时也会归为空值。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "修改个人资料")
public class UserProfileUpdateDto {
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

    /** 编辑版本，十进制字符串。 */
    @Schema(
            description = "编辑版本",
            minLength = 1,
            maxLength = 19,
            pattern = "^[0-9]+$",
            nullable = false,
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String version;
}
