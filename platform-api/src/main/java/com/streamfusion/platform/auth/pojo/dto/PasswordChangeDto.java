package com.streamfusion.platform.auth.pojo.dto;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;
import lombok.ToString;

/** 当前登录用户修改密码的参数。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "当前登录用户修改密码的参数")
public class PasswordChangeDto {
    /** 当前密码，仅用于后端身份核验。 */
    @JsonProperty(access = JsonProperty.Access.WRITE_ONLY)
    @ToString.Exclude
    @Schema(
            description = "当前密码",
            format = "password",
            accessMode = Schema.AccessMode.WRITE_ONLY,
            nullable = false,
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String currentPassword;

    /** 新密码，必须符合当前统一密码规则。 */
    @JsonProperty(access = JsonProperty.Access.WRITE_ONLY)
    @ToString.Exclude
    @Schema(
            description = "新密码",
            format = "password",
            accessMode = Schema.AccessMode.WRITE_ONLY,
            nullable = false,
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String newPassword;
}
