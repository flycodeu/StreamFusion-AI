package com.streamfusion.platform.apidocs.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;

@Schema(description = "接口文档可用状态")
public record ApiDocsStatusVo(
        @Schema(description = "OpenAPI 文档与 Swagger UI 是否均已启用") boolean enabled) {}
