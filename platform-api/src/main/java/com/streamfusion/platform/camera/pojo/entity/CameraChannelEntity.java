package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** A stable, independently authorized picture. Profiles never create another channel identity. */
@Getter
@Setter
@TableName("camera_channel")
public class CameraChannelEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long sourceId;
    private Long deviceId;
    private String externalChannelKey;
    private String externalChannelRef;
    private Long groupId;
    private Long defaultPreviewProfileId;
    private String name;
    private String sourceName;
    private String remark;
    private String lifecycle;
    private String mappingOrigin;
    private LocalDateTime catalogObservedAt;
    private Long version;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private Long createdBy;
    private Long updatedBy;
}
