package com.streamfusion.platform.apidocs.controller;

import com.streamfusion.platform.apidocs.pojo.vo.ApiDocsStatusVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api-docs")
@ModuleAccess("api-docs")
@Tag(name = "接口文档")
public class ApiDocsController {
    private final boolean enabled;

    public ApiDocsController(
            @Value("${springdoc.api-docs.enabled:true}") boolean apiDocsEnabled,
            @Value("${springdoc.swagger-ui.enabled:true}") boolean swaggerUiEnabled) {
        enabled = apiDocsEnabled && swaggerUiEnabled;
    }

    @GetMapping("/status")
    @Operation(summary = "查询接口文档可用状态")
    public R<ApiDocsStatusVo> status() {
        return R.success(new ApiDocsStatusVo(enabled));
    }
}
