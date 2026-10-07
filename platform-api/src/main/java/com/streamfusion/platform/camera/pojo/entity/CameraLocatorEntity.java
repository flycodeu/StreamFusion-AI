package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.*;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_profile_locator")
public class CameraLocatorEntity {
    @TableId(type = IdType.INPUT)
    private Long profileId;

    private Long sourceId;
    private String locatorKind;
    private String endpointPurpose;
    private String rtspHostMode;
    private String rtspHost;
    private Integer rtspPort;
    private byte[] rtspSecretCiphertext;
    private byte[] rtspSecretNonce;
    private byte[] rtspSecretTag;
    private String rtspEncryptionKeyId;
    private String rtspTransport;
    private byte[] protocolSecretCiphertext;
    private byte[] protocolSecretNonce;
    private byte[] protocolSecretTag;
    private String protocolEncryptionKeyId;
    private byte[] identityDigest;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
}
