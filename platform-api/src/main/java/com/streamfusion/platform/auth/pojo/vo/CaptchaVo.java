package com.streamfusion.platform.auth.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

@Schema(description = "一次性登录验证码，不包含答案")
public record CaptchaVo(
        @Schema(description = "验证码ID，32位十六进制") String captchaId,
        @Schema(description = "有效期截止时间，UTC") Instant expiresAt,
        @Schema(description = "当前会话专用PNG图片地址") String imageUrl) {}
