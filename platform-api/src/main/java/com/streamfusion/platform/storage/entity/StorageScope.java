package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_scope")
public class StorageScope {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long instanceId;
    private String bucket;
    private String prefix;
    private String displayName;
    private String probePrefix;
    private byte[] identityDigest;
    private String managementMode;
    private String observation;
    private Long version;
}
