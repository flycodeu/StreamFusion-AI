package com.streamfusion.platform.user.pojo.enums;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.RequiredArgsConstructor;

/** 用户账号状态。 */
@Getter
@RequiredArgsConstructor
@Schema(description = "用户账号状态")
public enum UserStatus {
    /** 待改密。 */
    PENDING_PASSWORD(0, "待改密"),

    /** 正常。 */
    NORMAL(1, "正常"),

    /** 封禁。 */
    BANNED(2, "封禁");

    /** 数据库存储编码。 */
    @Schema(description = "数据库存储编码")
    private final int code;

    /** 中文说明。 */
    @Schema(description = "中文说明")
    private final String description;

    public static boolean isValid(Integer code) {
        if (code == null) return false;
        for (UserStatus value : values()) {
            if (value.code == code) return true;
        }
        return false;
    }
}
