package com.streamfusion.platform.camera.status;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "指定码流和状态层的手动检查，不接受连接或凭据")
public record CameraStatusCheckCommand(
        @Schema(description = "当前通道内明确选定的码流ID") String streamProfileId,
        @Schema(
                        description = "检查层：CONTROL连接检查/MEDIA收流检查",
                        requiredMode = Schema.RequiredMode.REQUIRED)
                CameraStatusLayer layer,
        @Schema(description = "用户内幂等请求键：13位Unix毫秒-UUIDv4") String clientRequestId) {}
