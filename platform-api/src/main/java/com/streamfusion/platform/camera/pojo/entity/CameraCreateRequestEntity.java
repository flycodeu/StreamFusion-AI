package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.*;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_create_request")
public class CameraCreateRequestEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long actorUserId;
    private byte[] actorSessionDigest;
    private String sessionDigestKeyId;
    private String clientRequestId;
    private String operation;
    private Long parentResourceId;
    private byte[] requestFingerprint;
    private String fingerprintKeyId;
    private Long resourceId;
    private String resultSummary;
    private LocalDateTime requestedAt;
    private LocalDateTime createdAt;
    private LocalDateTime expiresAt;
}
