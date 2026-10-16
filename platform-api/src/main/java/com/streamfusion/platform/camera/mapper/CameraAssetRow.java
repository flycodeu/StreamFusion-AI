package com.streamfusion.platform.camera.mapper;

import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import lombok.Getter;
import lombok.Setter;

/** Joined read projection, never returned directly from a controller. */
@Getter
@Setter
public class CameraAssetRow extends CameraChannelEntity {
    private String sourceType;
    private String connectionCategory;
    private String vendorHint;
    private String sourceDisplayName;
    private Long sourceVersion;
    private String deviceType;
    private String manufacturer;
    private String model;
}
