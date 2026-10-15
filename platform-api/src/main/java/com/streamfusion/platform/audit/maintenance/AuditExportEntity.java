package com.streamfusion.platform.audit.maintenance;

import com.baomidou.mybatisplus.annotation.FieldStrategy;
import com.baomidou.mybatisplus.annotation.TableField;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("sys_audit_export")
public class AuditExportEntity {
    @TableId private Long id;
    private Long actorId;
    private String clientRequestId;
    private String filterJson;
    private String format;
    private String state;
    private String storageKey;
    private LocalDateTime upperTime;
    private Long upperId;
    private Integer rowCount;
    private Long fileBytes;
    private Boolean fileRemoved;
    private String reasonCode;
    private LocalDateTime createdAt;
    private LocalDateTime startedAt;
    private LocalDateTime finishedAt;
    private LocalDateTime expiresAt;

    @TableField(updateStrategy = FieldStrategy.ALWAYS)
    private LocalDateTime leaseUntil;

    private String workerId;
}
