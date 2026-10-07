package com.streamfusion.platform.camera.mapper;

import lombok.Getter;
import lombok.Setter;

/** A database aggregate of visible channels in one direct group. */
@Getter
@Setter
public class CameraGroupCountRow {
    private Long groupId;
    private Long cameraCount;
}
