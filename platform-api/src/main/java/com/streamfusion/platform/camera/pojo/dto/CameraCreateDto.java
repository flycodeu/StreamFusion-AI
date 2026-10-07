package com.streamfusion.platform.camera.pojo.dto;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@Schema(description = "创建待归档相机；手工设备建档可不提供码流")
public class CameraCreateDto {
    @Schema(description = "显式复用已保存且启用的来源时必填，与connection互斥", nullable = true)
    private String sourceId;

    @Schema(description = "显式复用来源时必填的编辑版本，与connection互斥", nullable = true)
    private String sourceVersion;

    @JsonInclude(JsonInclude.Include.NON_NULL)
    @Schema(description = "内联手工设备连接，与sourceId/sourceVersion互斥；仅保存不联网", nullable = true)
    private CameraManualConnectionDto connection;

    @Schema(description = "创建幂等键：13位Unix毫秒时间戳-UUIDv4", requiredMode = Schema.RequiredMode.REQUIRED)
    private String clientRequestId;

    @Schema(description = "本地相机名称", maxLength = 128, requiredMode = Schema.RequiredMode.REQUIRED)
    private String name;

    @Schema(description = "本地备注，不包含账号密码或完整视频地址", maxLength = 500, nullable = true)
    private String remark;

    @Schema(description = "初始手工RTSP码流，0至8项；内联设备建档必须省略或为空", nullable = true)
    private List<CameraInitialProfileDto> profiles;

    @Schema(description = "初始默认码流的clientKey；不传表示不设置默认值", nullable = true)
    private String defaultProfileClientKey;
}
