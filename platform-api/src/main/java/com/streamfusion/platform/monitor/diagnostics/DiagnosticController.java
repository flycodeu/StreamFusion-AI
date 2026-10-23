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
            description =
                    "按时间、请求标识或检查任务筛选，倒序游标分页，每页最多200条、跨度最多7天。total为匹配计数；totalExact为false时仅表示扫描范围内的下限，分页和计数各受16MiB及2秒扫描预算约束。")
    public R<DiagnosticQueryService.Result> events(
            @ParameterObject @ModelAttribute DiagnosticQueryService.Query query) {
        return R.success(service.query(query));
    }
}
