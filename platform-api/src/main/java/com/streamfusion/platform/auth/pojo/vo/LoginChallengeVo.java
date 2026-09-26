package com.streamfusion.platform.auth.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "一次性登录加密挑战")
public record LoginChallengeVo(
        @Schema(description = "一次性挑战ID") String challengeId,
        @Schema(description = "服务端P-256公钥，base64url") String serverPublicKey) {}
