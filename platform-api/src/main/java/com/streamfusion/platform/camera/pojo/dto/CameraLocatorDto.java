package com.streamfusion.platform.camera.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;

public record CameraLocatorDto(
        @Schema(description = "SOURCE使用来源地址，EXPLICIT使用本Profile地址") String hostMode,
        @Schema(description = "无凭据、路径、端口的目标IP或批准域名") String host,
        @Schema(description = "显式端口，1至65535") Integer port,
        @Schema(description = "编码后绝对路径的秘密写入动作") SecretWrite pathSecret,
        @Schema(description = "原始查询串的秘密写入动作，无开头问号") SecretWrite querySecret,
        @Schema(description = "首期固定TCP") String transport,
        @Schema(description = "完整RTSP地址；不含账号密码，与拆分定位字段互斥，秘密只写不回显") String fullUrl) {
    public CameraLocatorDto(
            String hostMode,
            String host,
            Integer port,
            SecretWrite pathSecret,
            SecretWrite querySecret,
            String transport) {
        this(hostMode, host, port, pathSecret, querySecret, transport, null);
    }

    @Override
    public String toString() {
        return "CameraLocatorDto[redacted]";
    }
}
