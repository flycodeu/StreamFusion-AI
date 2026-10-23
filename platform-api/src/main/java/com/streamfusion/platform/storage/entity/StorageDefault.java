package com.streamfusion.platform.storage.entity;

import com.baomidou.mybatisplus.annotation.*;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("storage_default")
public class StorageDefault {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long scopeId;
    private Long version;
}
