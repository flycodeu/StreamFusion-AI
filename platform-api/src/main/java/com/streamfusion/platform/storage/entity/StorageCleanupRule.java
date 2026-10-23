package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_cleanup_rule")
public class StorageCleanupRule {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long scopeId;
    private String name;
    private String relativePrefix;
    private Integer retentionDays;
    private String scheduleTime;
    private Integer maxDeleteObjects;
    private Long maxDeleteBytes;
    private Boolean enabled;
    private Long actorId;
    private Long version;
    private java.time.LocalDateTime nextAt;
    private String lastScheduleDate;
}
