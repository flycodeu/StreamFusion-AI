package com.streamfusion.platform.camera.status;

import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
public class CameraStatusSnapshotEntity {
    private Long profileId;

    private Long cameraId;
    private String layer;
    private String probeKind;

    private String value;

    private String reasonCode;
    private LocalDateTime observedAt;
    private LocalDateTime stateSince;

    private LocalDateTime lastAccessibleAt;

    private LocalDateTime lastFailedAt;

    private LocalDateTime readAt;
    private LocalDateTime validUntil;
    private LocalDateTime mediaWindowStartedAt;
    private LocalDateTime mediaWindowEndedAt;
    private Long mediaFirstFrames;
    private Long mediaLastFrames;
    private Integer mediaSampleCount;
    private Long mediaMaxGapMs;
    private LocalDateTime checkRequestedAt;
    private Long checkId;
    private String connectionKeyId;
    private byte[] connectionDigest;
    private LocalDateTime updatedAt;
}
