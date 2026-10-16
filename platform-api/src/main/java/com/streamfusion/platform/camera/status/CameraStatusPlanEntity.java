package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.annotation.TableField;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_status_plan")
public class CameraStatusPlanEntity {
    @TableId private Long id;
    private Long actorUserId;
    private String clientRequestId;
    private String requestKeyId;
    private byte[] requestFingerprint;
    private String state;
    private String layer;
    private String dispatchMode;
    private Integer parallelism;
    private Integer mediaTargetCount;
    private Integer intervalMinutes;
    private Integer targetCount;
    private LocalDateTime startAt;
    private LocalDateTime endAt;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private Long version;

    @TableField(exist = false)
    private Integer blockedCount;

    @TableField(exist = false)
    private LocalDateTime nextAt;

    @TableField(exist = false)
    private String lastReasonCode;

    @TableField(exist = false)
    private Integer successCount;

    @TableField(exist = false)
    private Integer failedCount;

    @TableField(exist = false)
    private Integer pendingCount;

    @TableField(exist = false)
    private Integer skippedCount;

    @TableField(exist = false)
    private Integer unknownCount;
}
