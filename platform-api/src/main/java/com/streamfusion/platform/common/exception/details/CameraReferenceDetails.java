package com.streamfusion.platform.common.exception.details;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

/** Only reference categories and counts; never reveals another user's identity. */
@Schema(description = "阻止删除或修改的相机引用摘要")
public record CameraReferenceDetails(
        @Schema(description = "当前调用人可见的引用类型及数量，不含其他账户身份") List<Reference> references) {
    @Schema(description = "单类资源引用计数")
    public record Reference(
            @Schema(
                            description =
                                    "引用类型，例如DIRECT_CAMERA_GRANT、DEFAULT_PREVIEW_PROFILE、CAMERA_CHANNEL")
                    String type,
            @Schema(description = "该类型的引用数量", minimum = "1") long count) {}
}
