package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import lombok.Getter;
import lombok.Setter;

/** Local pagination projection; never exposed as an API entity. */
@Getter
@Setter
public class CameraStatusPageRow extends CameraChannelEntity {
    private String deviceName;
    private String connectionCategory;
    private String addressHost;
    private Integer addressPort;
}
