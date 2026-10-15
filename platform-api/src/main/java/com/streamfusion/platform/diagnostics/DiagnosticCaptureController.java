package com.streamfusion.platform.diagnostics;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/diagnostics/capture")
@ModuleAccess("diagnostics")
@Tag(name = "临时详细诊断")
@RequiredArgsConstructor
public class DiagnosticCaptureController {
    private final DiagnosticCaptureService service;

    @GetMapping
    @Operation(summary = "读取本机临时详细采集状态")
    public R<DiagnosticCaptureService.View> get() {
        return R.success(service.get());
    }

    @GetMapping("/routes")
    @Operation(summary = "查询允许采集的接口")
    public R<List<DiagnosticCaptureService.Route>> routes() {
        return R.success(service.routes());
    }

    @PostMapping
    @Operation(summary = "开始有期限和条数上限的详细采集")
    public R<DiagnosticCaptureService.View> start(
            @RequestBody DiagnosticCaptureService.Command command, HttpServletRequest request) {
        return R.success(service.start(command, AuditContextDto.from(request)));
    }

    @DeleteMapping
    @Operation(summary = "停止详细采集")
    public R<DiagnosticCaptureService.View> stop(HttpServletRequest request) {
        return R.success(service.stop(AuditContextDto.from(request)));
    }
}
