package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.Map;

@Schema(description = "相机或视频组变更的授权影响预览")
public record CameraImpactVo(
        @Schema(description = "相机ID，视频组移组预览时为空") String cameraId,
        @Schema(description = "原分组ID或原上级视频组ID") String fromGroupId,
        @Schema(description = "目标分组ID或目标上级视频组ID") String targetGroupId,
        @Schema(description = "相机提交后的生命周期，视频组移组时为空字符串") String targetLifecycle,
        @Schema(description = "原视频组路径") String fromPath,
        @Schema(description = "目标视频组路径") String toPath,
        @Schema(description = "此次变更涉及的相机数量") long affectedCameraCount,
        @Schema(description = "数据范围变化人数摘要：gainedUserCount、lostUserCount及computedAt，不代表观看人数")
                Map<String, Object> authorizationImpact,
        @Schema(description = "绑定当前账户会话、对象版本和影响状态的确认凭据") String confirmation,
        @Schema(description = "确认凭据到期时间，UTC") Instant expiresAt) {}
