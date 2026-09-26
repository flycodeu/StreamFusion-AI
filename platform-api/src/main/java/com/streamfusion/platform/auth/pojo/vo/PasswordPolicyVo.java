package com.streamfusion.platform.auth.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

public record PasswordPolicyVo(
        @Schema(description = "最小Unicode码点长度") int minLength,
        @Schema(description = "最大Unicode码点长度") int maxLength,
        @Schema(description = "是否要求英文大写字母") boolean requireUppercase,
        @Schema(description = "是否要求英文小写字母") boolean requireLowercase,
        @Schema(description = "是否要求数字") boolean requireDigit,
        @Schema(description = "是否要求符号") boolean requireSymbol) {}
