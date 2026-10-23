package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_history_policy")
public class StorageHistoryPolicy {
    @TableId(type = IdType.INPUT)
    private Long id;

    private Boolean inheritGlobal;
    private Boolean enabled;

    @TableField(updateStrategy = FieldStrategy.ALWAYS)
    private Integer retentionDays;

    private Long version;
    private Long updatedBy;
    private java.time.LocalDateTime updatedAt;
}
