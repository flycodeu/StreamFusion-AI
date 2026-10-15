package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.annotation.FieldStrategy;
import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableField;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName(value = "camera_status_snapshot", autoResultMap = true)
public class CameraStatusSnapshotEntity {
    @TableId(type = IdType.INPUT)
    private Long profileId;

    private Long cameraId;
    private String layer;
    private String probeKind;

    @TableField("observation_value")
    private String value;

    private String reasonCode;
    private LocalDateTime observedAt;
    private LocalDateTime stateSince;

    @TableField(updateStrategy = FieldStrategy.ALWAYS)
    private LocalDateTime lastAccessibleAt;

    @TableField(updateStrategy = FieldStrategy.ALWAYS)
    private LocalDateTime lastFailedAt;

    private LocalDateTime readAt;
    private LocalDateTime validUntil;
    private LocalDateTime checkRequestedAt;
    private Long checkId;
    private String connectionKeyId;
    private byte[] connectionDigest;
    private LocalDateTime updatedAt;
}
