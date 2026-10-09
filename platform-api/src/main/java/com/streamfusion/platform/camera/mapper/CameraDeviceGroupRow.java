package com.streamfusion.platform.camera.mapper;

import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
public class CameraDeviceGroupRow {
    private String groupKey;
    private String name;
    private boolean identified;
    private String manufacturer;
    private String model;
    private String sourceDisplayName;
    private String sourceType;
    private String connectionCategory;
    private long channelCount;
    private long enabledCount;
    private long disabledCount;
    private long pendingCount;
    private Long groupId;
    private long groupCount;
}
