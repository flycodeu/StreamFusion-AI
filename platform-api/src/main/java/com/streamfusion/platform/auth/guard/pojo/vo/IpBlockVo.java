package com.streamfusion.platform.auth.guard.pojo.vo;

import com.streamfusion.platform.audit.pojo.vo.AuditReferenceVo;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

public record IpBlockVo(
        @Schema(description = "记录ID") String id,
        @Schema(description = "规范化来源IP") String sourceIp,
        @Schema(description = "BLOCKED封禁、RELEASED解除") String status,
        @Schema(description = "封禁原因编码") String reasonCode,
        @Schema(description = "触发封禁时的失败次数") int failedAttempts,
        @Schema(description = "失败计数窗口秒数") int windowSeconds,
        @Schema(description = "最近封禁时间") Instant blockedAt,
        @Schema(description = "最近解除时间，未解除时为空") Instant unblockedAt,
        @Schema(description = "解除操作人历史ID，未解除时为空") String unblockedBy,
        @Schema(description = "解除操作人可读身份及名称来源，未解除时为空") AuditReferenceVo unblockedByReference,
        @Schema(description = "并发版本的十进制字符串") String version) {}
