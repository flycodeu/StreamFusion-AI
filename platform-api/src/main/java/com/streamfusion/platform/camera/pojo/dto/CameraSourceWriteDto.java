package com.streamfusion.platform.camera.pojo.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

public record CameraSourceWriteDto(
        @Schema(description = "创建幂等键，13位毫秒时间戳加UUIDv4") String clientRequestId,
        @Schema(description = "接入来源名称") String name,
        @Schema(description = "已注册的接入驱动；手工DEVICE来源可未知", nullable = true) String adapterType,
        @Schema(description = "部署批准的目标网络策略键；手工DEVICE存档可空，实际外呼必需", nullable = true)
                String networkPolicyKey,
        @Schema(description = "是否允许新使用") Boolean enabled,
        @Schema(description = "本地备注，可清空") String remark,
        @Schema(description = "与接入协议匹配的固定用途凭据，最多一项") List<Credential> credentials,
        @Schema(description = "与接入协议匹配的完整端点配置，恰好一项") List<Endpoint> endpoints,
        @JsonInclude(JsonInclude.Include.NON_NULL)
                @Schema(description = "连接类别DEVICE、PLATFORM或RTSP；旧请求从驱动推导", nullable = true)
                String connectionCategory,
        @JsonInclude(JsonInclude.Include.NON_NULL)
                @Schema(description = "人工厂商提示，不作为观测制造商", nullable = true)
                String vendorHint) {
    public CameraSourceWriteDto(
            String clientRequestId,
            String name,
            String adapterType,
            String networkPolicyKey,
            Boolean enabled,
            String remark,
            List<Credential> credentials,
            List<Endpoint> endpoints) {
        this(
                clientRequestId,
                name,
                adapterType,
                networkPolicyKey,
                enabled,
                remark,
                credentials,
                endpoints,
                null,
                null);
    }

    public record Credential(
            @Schema(description = "固定用途RTSP、DEVICE_HTTP、ONVIF、VENDOR_HTTP或PLATFORM_HTTP")
                    String purpose,
            @Schema(description = "账号写入动作，旧值不回显") SecretWrite username,
            @Schema(description = "密码写入动作，旧值不回显") SecretWrite password) {
        @Override
        public String toString() {
            return "Credential[redacted]";
        }
    }

    public record Endpoint(
            @Schema(description = "与接入协议匹配的固定端点用途") String purpose,
            @Schema(description = "RTSP来源为rtsp，控制端点为http或https") String scheme,
            @Schema(description = "无凭据、路径、端口的目标IP或批准域名") String host,
            @Schema(description = "显式端口，1至65535") Integer port,
            @Schema(description = "RTSP和厂商HTTP为空串，ONVIF为/onvif/device_service，平台为/artemis")
                    String basePath,
            @Schema(description = "NONE或DRIVER_NEGOTIATED") String authMode,
            @Schema(description = "绑定同来源且相同用途的凭据；匿名时为空，PLATFORM来源必须配置凭据") String credentialPurpose,
            @Schema(description = "本阶段固定SYSTEM_CA") String tlsPolicy) {}

    @Override
    public String toString() {
        return "CameraSourceWriteDto[redacted]";
    }
}
