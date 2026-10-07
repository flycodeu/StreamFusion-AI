package com.streamfusion.platform.camera.access.pojo;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** Durable, bounded discovery ticket. No plaintext credentials, URLs or session identifiers. */
@Getter
@Setter
@TableName("camera_access_job")
public class CameraAccessJobEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long actorUserId;
    private byte[] actorSessionDigest;
    private String digestKeyId;
    private String clientRequestId;
    private byte[] requestFingerprint;
    private String status;
    private String method;
    private String jobKind;
    private Long sourceId;
    private Long sourceVersion;
    private Long bulkGroupId;
    private Integer bulkNextPage;
    private Long bulkTotal;
    private Integer bulkProcessedCount;
    private Integer bulkCreatedCount;
    private Integer bulkExistingCount;
    private Integer bulkFailedCount;
    private Integer bulkDuplicateCount;
    private LocalDateTime bulkStartedAt;
    private Long version;
    private String encryptionKeyId;
    private byte[] secretNonce;
    private byte[] secretCiphertext;
    private byte[] secretTag;
    private String reasonCode;
    private String originTraceId;
    private String resultSummary;
    private String importRequestId;
    private byte[] importFingerprint;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private LocalDateTime expiresAt;
}
