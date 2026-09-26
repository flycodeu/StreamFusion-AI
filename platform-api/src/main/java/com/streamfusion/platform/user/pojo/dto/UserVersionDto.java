package com.streamfusion.platform.user.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 带版本的用户操作请求。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@Schema(description = "带版本的用户操作请求")
public class UserVersionDto {
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
