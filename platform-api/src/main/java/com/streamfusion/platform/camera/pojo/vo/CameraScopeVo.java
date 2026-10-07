package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;
import java.util.Map;

@Schema(description = "账户相机数据授权聚合")
public record CameraScopeVo(
        @Schema(description = "目标账户ID") String userId,
        @Schema(description = "授权聚合编辑版本") String version,
        @Schema(description = "授权模式，超级管理员为ALL，其他账户为CUSTOM") String mode,
        @Schema(description = "显式授权的视频组ID集合") List<String> groupIds,
        @Schema(description = "显式授权的相机ID集合") List<String> cameraIds,
        @Schema(description = "显式视频组授权详情，含groupId、name、path") List<Map<String, Object>> groupGrants,
        @Schema(description = "显式相机授权详情，含cameraId、name、groupId、path、lifecycle")
                List<Map<String, Object>> cameraGrants,
        @Schema(description = "当前有效数据范围统计，含effectiveCameraCount、enabledCameraCount、computedAt")
                Map<String, Object> effectiveSummary,
        @Schema(description = "最后修改时间，尚未保存时为空，UTC") Instant updatedAt,
        @Schema(description = "最后修改账户ID，尚未保存时为空") String updatedBy) {}
