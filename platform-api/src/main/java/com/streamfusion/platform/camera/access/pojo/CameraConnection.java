package com.streamfusion.platform.camera.access.pojo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

/** Transient connection input; stored only inside the expiring encrypted job envelope. */
@Schema(description = "设备连接配置；凭据和完整RTSP地址只写不回显")
public record CameraConnection(
        String method,
        String name,
        String host,
        Integer port,
        String scheme,
        String username,
        String password,
        Integer rtspPort,
        String networkPolicyKey,
        String sourceId,
        String sourceVersion,
        List<String> rtspUrls,
        @Schema(description = "平台目录页码，从1开始；非分页驱动仅允许1")
                @com.fasterxml.jackson.annotation.JsonInclude(
                        com.fasterxml.jackson.annotation.JsonInclude.Include.NON_NULL)
                Integer pageNumber,
        @Schema(description = "平台目录每页条数，1到100；非分页驱动使用100")
                @com.fasterxml.jackson.annotation.JsonInclude(
                        com.fasterxml.jackson.annotation.JsonInclude.Include.NON_NULL)
                Integer pageSize) {
    public CameraConnection(
            String method,
            String name,
            String host,
            Integer port,
            String scheme,
            String username,
            String password,
            Integer rtspPort,
            String networkPolicyKey,
            String sourceId,
            String sourceVersion,
            List<String> rtspUrls) {
        this(
                method,
                name,
                host,
                port,
                scheme,
                username,
                password,
                rtspPort,
                networkPolicyKey,
                sourceId,
                sourceVersion,
                rtspUrls,
                null,
                null);
    }

    @Override
    public String toString() {
        return "CameraConnection[redacted]";
    }
}
