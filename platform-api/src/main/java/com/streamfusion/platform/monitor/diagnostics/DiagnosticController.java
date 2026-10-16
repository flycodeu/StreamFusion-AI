package com.streamfusion.platform.monitor.diagnostics;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/diagnostics")
@ModuleAccess("diagnostics")
@Tag(name = "诊断日志")
@RequiredArgsConstructor
public class DiagnosticController {
    private final DiagnosticQueryService service;

    @GetMapping("/events")
    @Operation(
            summary = "查询本机接口和相机检查诊断日志",
            description = "按时间、请求标识或检查任务筛选，最多返回200条；跨度最多7天，查询受扫描上限约束。")
    public R<DiagnosticQueryService.Result> events(
            @ParameterObject @ModelAttribute DiagnosticQueryService.Query query) {
        return R.success(service.query(query));
    }
}
