package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "授权相机的接入地址，不含凭据、路径或查询参数")
public record CameraAddressVo(
        @Schema(description = "DEVICE设备地址、PLATFORM平台地址或RTSP接入地址") String kind,
        @Schema(description = "配置的IP或主机名，不执行DNS解析") String host,
        @Schema(description = "接入端口", nullable = true) Integer port) {
    public static CameraAddressVo of(String category, String host, Integer port) {
        if (host == null || host.isBlank()) return null;
        return new CameraAddressVo(category, host, port);
    }
}
