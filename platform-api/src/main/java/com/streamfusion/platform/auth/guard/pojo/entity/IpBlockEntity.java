package com.streamfusion.platform.auth.guard.pojo.entity;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("sys_ip_block")
public class IpBlockEntity {
    @TableId private Long id;
    private String sourceIp;
    private String status;
    private String reasonCode;
    private Integer failedAttempts;
    private Integer windowSeconds;
    private LocalDateTime blockedAt;
    private LocalDateTime unblockedAt;
    private Long unblockedBy;
    private Long version;
}
