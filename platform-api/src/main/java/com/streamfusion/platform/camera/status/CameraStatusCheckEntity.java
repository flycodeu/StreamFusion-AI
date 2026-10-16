package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** No URI, plaintext credentials, session secret or upstream response is persisted. */
@Getter
@Setter
@TableName("camera_status_check")
public class CameraStatusCheckEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long actorUserId;
    private Long planId;
    private Long cameraId;
    private Long sourceId;
    private Long profileId;
    private String clientRequestId;
    private String requestKeyId;
    private byte[] requestFingerprint;
    private String sessionKeyId;
    private byte[] sessionDigest;
    private String connectionKeyId;
    private byte[] connectionDigest;
    private String layer;
    private String probeKind;
    private String executionState;
    private String resultValue;
    private String reasonCode;
    private String claimToken;
    private String traceId;
    private LocalDateTime requestedAt;
    private LocalDateTime startedAt;
    private LocalDateTime finishedAt;
    private LocalDateTime deadlineAt;
    private LocalDateTime observedAt;
    private LocalDateTime readAt;
    private LocalDateTime validUntil;
    private LocalDateTime mediaWindowStartedAt;
    private LocalDateTime mediaWindowEndedAt;
    private Long mediaFirstFrames;
    private Long mediaLastFrames;
    private Integer mediaSampleCount;
    private Long mediaMaxGapMs;
    private LocalDateTime updatedAt;
    private LocalDateTime retainUntil;
    private Long durationMs;
    private Boolean snapshotApplied;
    private Long version;
}
