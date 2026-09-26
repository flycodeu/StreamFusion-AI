package com.streamfusion.platform.loginrecord.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** Historical successful session, independent of user deletion and with no session credentials. */
@Getter
@Setter
@TableName("sys_login_record")
public class LoginRecordEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long userId;
    private String username;
    private String nickname;
    private Long sessionVersion;
    private String sourceIp;
    private String regionType;
    private String region;
    private String browser;
    private String os;
    private LocalDateTime loginAt;
    private LocalDateTime lastActivityAt;
    private LocalDateTime absoluteExpiresAt;
    private Integer idleTimeoutSeconds;
    private Integer activityIntervalSeconds;
    private LocalDateTime endedAt;
    private String endReason;
}
