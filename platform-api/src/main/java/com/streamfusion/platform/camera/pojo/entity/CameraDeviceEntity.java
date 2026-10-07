package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** Local device observations; this object contains no network address or credential. */
@Getter
@Setter
@TableName("camera_device")
public class CameraDeviceEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long sourceId;
    private String externalDeviceKey;
    private String externalDeviceRef;
    private String deviceType;
    private String sourceName;
    private String manufacturer;
    private String model;
    private String serialNumber;
    private String firmwareVersion;
    private LocalDateTime infoObservedAt;
    private Long version;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private Long createdBy;
    private Long updatedBy;
}
