package com.streamfusion.platform.auth.pojo.dto;

import com.fasterxml.jackson.annotation.JsonProperty;
import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;
import lombok.ToString;

/** 账号密码登录参数。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "账号密码登录参数")
public class LoginDto {
    /** 登录账号，仅允许4至32位英文字母和数字。 */
    @Schema(
            description = "登录账号",
            minLength = 4,
            maxLength = 32,
            pattern = "^[A-Za-z0-9]{4,32}$",
            nullable = false,
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String username;

    /** 登录密码，仅用于请求输入，不参与响应或对象日志输出。 */
    @JsonProperty(access = JsonProperty.Access.WRITE_ONLY)
    @ToString.Exclude
    @Schema(
            description = "当前账号密码",
            format = "password",
            accessMode = Schema.AccessMode.WRITE_ONLY,
            nullable = false,
            requiredMode = Schema.RequiredMode.REQUIRED)
    private String password;
}
