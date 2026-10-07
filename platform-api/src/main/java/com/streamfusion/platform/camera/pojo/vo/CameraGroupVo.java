package com.streamfusion.platform.camera.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;

@Schema(description = "视频分组及当前用户范围内统计")
public record CameraGroupVo(
        @Schema(description = "视频组ID") String groupId,
        @Schema(description = "上级视频组ID，根组为空") String parentId,
        @Schema(description = "视频组名称") String name,
        @Schema(description = "非负排序值") int sortOrder,
        @Schema(description = "备注") String remark,
        @Schema(description = "编辑版本") String version,
        @Schema(description = "是否有当前账户可见的直接下级组") boolean hasChildren,
        @Schema(description = "本组及后代范围内可见的已归档相机数") long visibleCameraCount,
        @Schema(description = "统计观测时间，UTC") Instant countObservedAt) {}
