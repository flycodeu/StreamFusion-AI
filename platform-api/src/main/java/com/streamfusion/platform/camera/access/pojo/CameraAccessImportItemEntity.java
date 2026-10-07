package com.streamfusion.platform.camera.access.pojo;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** A bounded, non-secret per-resource receipt; asset and counter commit in the same transaction. */
@Getter
@Setter
@TableName("camera_access_import_item")
public class CameraAccessImportItemEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long jobId;
    private String externalKey;
    private Integer pageNumber;
    private Integer itemIndex;
    private String status;
    private Long cameraId;
    private String name;
    private String reasonCode;
    private LocalDateTime createdAt;
}
