package com.streamfusion.platform.auth.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;

/** One-time ECDH/AES-GCM login envelope. Values are unpadded base64url. */
@Schema(description = "加密登录报文")
public record EncryptedLoginDto(
        @Schema(description = "一次性挑战ID") String challengeId,
        @Schema(description = "客户端P-256公钥，base64url") String clientPublicKey,
        @Schema(description = "AES-GCM随机向量，base64url") String iv,
        @Schema(description = "加密的账号密码报文，base64url") String ciphertext) {}
