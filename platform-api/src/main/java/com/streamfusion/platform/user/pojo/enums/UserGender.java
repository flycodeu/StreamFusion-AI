package com.streamfusion.platform.user.pojo.enums;

import io.swagger.v3.oas.annotations.media.Schema;
import lombok.Getter;
import lombok.RequiredArgsConstructor;

/** 用户性别。 */
@Getter
@RequiredArgsConstructor
@Schema(description = "用户性别")
public enum UserGender {
    /** 未设置。 */
    UNKNOWN(0, "未设置"),

    /** 男。 */
    MALE(1, "男"),

    /** 女。 */
    FEMALE(2, "女");

    /** 数据库存储编码。 */
    @Schema(description = "数据库存储编码")
    private final int code;

    /** 中文说明。 */
    @Schema(description = "中文说明")
    private final String description;

    public static boolean isValid(Integer code) {
        if (code == null) return false;
        for (UserGender value : values()) {
            if (value.code == code) return true;
        }
        return false;
    }
}
