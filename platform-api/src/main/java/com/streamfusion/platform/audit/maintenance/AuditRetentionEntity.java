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
@TableName("sys_audit_retention_policy")
public class AuditRetentionEntity {
    @TableId private Long id;
    private Boolean enabled;
    private String mode;
    private Integer keepMonths;
    private Integer maxRecords;
    private Long version;
    private Long updatedBy;
    private LocalDateTime updatedAt;

    @TableField(updateStrategy = FieldStrategy.ALWAYS)
    private LocalDateTime nextAt;

    private LocalDateTime lastStartedAt;

    @TableField(updateStrategy = FieldStrategy.ALWAYS)
    private LocalDateTime lastFinishedAt;

    private Integer lastDeleted;
    private String lastResult;
}
