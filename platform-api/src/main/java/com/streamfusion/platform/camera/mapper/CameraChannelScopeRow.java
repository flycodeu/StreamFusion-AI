package com.streamfusion.platform.camera.mapper;

import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
public class CameraChannelScopeRow {
    private Long id;
    private Long groupId;
    private Long version;
    private String lifecycle;
    private String name;
}
