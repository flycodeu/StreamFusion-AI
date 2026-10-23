package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_cleanup_plan")
public class StorageCleanupPlan {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long instanceId;
    private Long scopeId;
    private Long actorId;
    private Long ruleId;
    private Long ruleVersion;
    private Long instanceVersion;
    private Long scopeVersion;
    private String mode;
    private String status;
    private String inputJson;
    private Long candidateCount;
    private Long candidateBytes;
    private Boolean limitReached;
    private Long executionTaskId;
    private Long version;
    private java.time.LocalDateTime createdAt;
    private java.time.LocalDateTime expiresAt;
}
