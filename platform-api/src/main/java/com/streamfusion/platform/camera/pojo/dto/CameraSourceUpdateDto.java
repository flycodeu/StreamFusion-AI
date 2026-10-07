package com.streamfusion.platform.camera.pojo.dto;

import com.fasterxml.jackson.annotation.JsonSetter;
import com.fasterxml.jackson.annotation.Nulls;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
public class CameraSourceUpdateDto {
    @Schema(description = "当前编辑版本，十进制字符串")
    private String version;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "接入来源名称")
    private String name;

    @Schema(description = "本地备注，可清空")
    private String remark;

    @Schema(description = "未绑定手工设备的驱动提示；显式null表示未知", nullable = true)
    private String adapterType;

    @Schema(description = "人工厂商提示，显式null清空", nullable = true)
    private String vendorHint;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "是否允许新使用")
    private Boolean enabled;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "部署批准的目标网络策略键")
    private String networkPolicyKey;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "控制协议来源使用的RTSP媒体端口，1至65535；RTSP来源直接使用端点端口")
    private Integer rtspPort;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "按用途完整替换端点的集合")
    private List<CameraSourceWriteDto.Endpoint> endpointsUpsert;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "显式移除用途集合；首期唯一RTSP端点不可移除")
    private List<String> endpointsRemove;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "账号和密码分别KEEP、REPLACE或成对CLEAR")
    private List<CameraSourceWriteDto.Credential> credentialsUpsert;

    @JsonSetter(nulls = Nulls.FAIL)
    @Schema(description = "显式移除凭据用途，最终不得仍被端点引用")
    private List<String> credentialsRemove;

    @Getter(lombok.AccessLevel.NONE)
    @Setter(lombok.AccessLevel.NONE)
    private boolean remarkProvided;

    @Getter(lombok.AccessLevel.NONE)
    @Setter(lombok.AccessLevel.NONE)
    private boolean adapterTypeProvided;

    @Getter(lombok.AccessLevel.NONE)
    @Setter(lombok.AccessLevel.NONE)
    private boolean vendorHintProvided;

    public boolean hasAdapterType() {
        return adapterTypeProvided;
    }

    public boolean hasVendorHint() {
        return vendorHintProvided;
    }

    @JsonSetter
    public void setAdapterType(String value) {
        adapterType = value;
        adapterTypeProvided = true;
    }

    @JsonSetter
    public void setVendorHint(String value) {
        vendorHint = value;
        vendorHintProvided = true;
    }

    public boolean hasRemark() {
        return remarkProvided;
    }

    @JsonSetter
    public void setRemark(String value) {
        remark = value;
        remarkProvided = true;
    }
}
