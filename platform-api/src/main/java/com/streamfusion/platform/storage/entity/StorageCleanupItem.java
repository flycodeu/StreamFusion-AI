package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_cleanup_item")
public class StorageCleanupItem {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long planId;
    private String objectKey;
    private String etag;
    private Long sizeBytes;
    private java.time.LocalDateTime lastModified;
    private String status;
    private String reasonCode;
}
