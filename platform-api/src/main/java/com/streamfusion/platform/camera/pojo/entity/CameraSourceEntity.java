package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.*;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_source")
public class CameraSourceEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private String name;
    private String connectionCategory;
    private String adapterType;
    private String vendorHint;
    private String vendorModelFamily;
    private String networkPolicyKey;
    private Integer rtspPort;
    private Boolean enabled;
    private String remark;
    private Long version;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private Long createdBy;
    private Long updatedBy;
}
