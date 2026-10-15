package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.annotation.FieldStrategy;
import com.baomidou.mybatisplus.annotation.TableField;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_status_plan_target")
public class CameraStatusPlanTargetEntity {
    @TableId private Long id;
    private Long planId;
    private Long cameraId;
    private Long profileId;
    private String connectionKeyId;
    private byte[] connectionDigest;
    private LocalDateTime nextAt;

    @TableField(updateStrategy = FieldStrategy.ALWAYS)
    private String reasonCode;

    private Boolean blocked;
    private Long lastCheckId;
}
