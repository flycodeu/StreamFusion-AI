package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_instance")
public class StorageInstance {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private String name;
    private String endpoint;
    private String region;
    private String networkPolicyKey;
    private String metricsEndpoint;
    private String managementUrl;
    private String remark;
    private String encryptionKeyId;
    private byte[] secretNonce;
    private byte[] secretCiphertext;
    private byte[] secretTag;
    private Boolean writeEnabled;
    private String observation;
    private String usageSnapshot;
    private String metricsSnapshot;
    private Long version;
    private Long createdBy;
    private java.time.LocalDateTime createdAt;
    private java.time.LocalDateTime updatedAt;
}
