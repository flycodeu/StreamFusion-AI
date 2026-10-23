package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_task")
public class StorageTask {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long runId;
    private Long instanceId;
    private Long scopeId;
    private Long actorId;
    private String type;
    private String status;
    private String phase;
    private String requestKey;
    private String requestFingerprint;
    private String inputJson;
    private String resultJson;
    private String cursorJson;
    private String traceId;
    private String owner;
    private java.time.LocalDateTime leaseUntil;
    private Boolean cancelRequested;
    private Long version;
    private java.time.LocalDateTime createdAt;
    private java.time.LocalDateTime startedAt;
    private java.time.LocalDateTime finishedAt;
}
