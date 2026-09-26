package com.streamfusion.platform.loginrecord.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

@Schema(description = "成功登录会话历史；活动时间经过节流，非精确前台使用时长")
public record LoginRecordVo(
        @Schema(description = "独立登录记录ID，十进制字符串") String id,
        @Schema(description = "历史用户ID，十进制字符串") String userId,
        @Schema(description = "登录时账号快照") String username,
        @Schema(description = "登录时昵称快照") String nickname,
        @Schema(description = "可信代理解析后的来源IP") String sourceIp,
        @Schema(description = "LOOPBACK/PRIVATE/LINK_LOCAL/LOCAL_NETWORK/PUBLIC/UNKNOWN")
                String regionType,
        @Schema(description = "本地分类或配置网段名称；不推测公网地理位置") String region,
        @Schema(description = "浏览器类别，基于客户端声明") String browser,
        @Schema(description = "操作系统类别，基于客户端声明") String os,
        @Schema(description = "成功登录时间") Instant loginAt,
        @Schema(description = "最后持久化活动时间，默认最多每60秒更新") Instant lastActivityAt,
        @Schema(description = "明确结束或推断会话失效时间") Instant endedAt,
        @Schema(description = "结束原因；仍有效时为空") String endReason,
        @Schema(description = "ACTIVE尚未判定失效、ENDED明确结束、EXPIRED期限已过") String status,
        @Schema(description = "登录至最后记录活动的秒数；主动退出更新最后活动，非前台使用时长") long durationSeconds) {}
