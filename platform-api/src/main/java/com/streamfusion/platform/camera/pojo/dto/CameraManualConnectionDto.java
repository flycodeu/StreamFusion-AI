package com.streamfusion.platform.camera.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;

/** A stored device connection; creating it never contacts the address. */
@Schema(description = "手工设备档案连接；仅保存，不读取设备或连接视频")
public record CameraManualConnectionDto(
        @Schema(description = "设备IP或主机名，不包含协议、端口和路径") String host,
        @Schema(description = "管理端口，省略使用80", nullable = true) Integer port,
        @Schema(description = "http或https，省略使用http", nullable = true) String scheme,
        @Schema(description = "可选的已注册设备驱动提示，未知时省略", nullable = true) String adapterType,
        @Schema(description = "人工填写的厂商提示，不是设备观测事实", nullable = true) String vendorHint,
        @Schema(description = "设备账号，只写不回显；与密码成对提供", nullable = true) String username,
        @Schema(description = "设备密码，只写不回显；与账号成对提供", nullable = true) String password) {
    @Override
    public String toString() {
        return "CameraManualConnectionDto[redacted]";
    }
}
