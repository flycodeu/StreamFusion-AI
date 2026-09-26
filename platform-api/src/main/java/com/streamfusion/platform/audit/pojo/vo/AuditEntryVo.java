package com.streamfusion.platform.audit.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

public record AuditEntryVo(
        @Schema(description = "审计记录 ID 的十进制字符串") String id,
        @Schema(description = "操作者历史 ID 的十进制字符串，未登录时为空") String actorId,
        @Schema(description = "操作者当前账号，账号已删除时为空") String username,
        @Schema(description = "操作者当前昵称，账号已删除时为空") String nickname,
        @Schema(description = "目标模块编码，如 USER、ROLE、MENU、DEPT") String module,
        @Schema(description = "操作目标 ID 的十进制字符串") String targetId,
        @Schema(description = "操作动作编码") String action,
        @Schema(description = "操作结果：SUCCESS、FAILURE、DENIED") String result,
        @Schema(description = "失败或拒绝的原因编码") String reasonCode,
        @Schema(description = "请求追踪标识") String traceId,
        @Schema(description = "操作发生时间") Instant createdAt,
        @Schema(description = "操作者可读身份及名称来源，未登录时为空") AuditReferenceVo actor,
        @Schema(description = "操作目标可读身份及名称来源，没有目标ID时为空") AuditReferenceVo target) {}
