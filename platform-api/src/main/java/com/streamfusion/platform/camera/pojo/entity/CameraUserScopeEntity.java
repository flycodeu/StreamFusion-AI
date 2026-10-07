package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.*;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_user_scope")
public class CameraUserScopeEntity {
    @TableId(value = "user_id", type = IdType.INPUT)
    private Long userId;

    private Long version;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private Long createdBy;
    private Long updatedBy;
}
