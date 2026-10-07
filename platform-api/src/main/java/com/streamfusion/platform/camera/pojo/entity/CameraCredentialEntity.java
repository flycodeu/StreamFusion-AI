package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.*;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_source_credential")
public class CameraCredentialEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long sourceId;
    private String purpose;
    private byte[] secretCiphertext;
    private byte[] secretNonce;
    private byte[] secretTag;
    private String encryptionKeyId;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
}
