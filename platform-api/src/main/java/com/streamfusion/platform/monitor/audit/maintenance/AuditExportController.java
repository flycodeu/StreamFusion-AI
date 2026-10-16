package com.streamfusion.platform.monitor.audit.maintenance;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.*;
import java.io.IOException;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/audit/exports")
@ModuleAccess("audit")
@Tag(name = "操作日志导出")
@RequiredArgsConstructor
public class AuditExportController {
    private final AuditExportService service;
    private final org.springframework.beans.factory.ObjectProvider<AuditMaintenanceWorker> worker;

    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(summary = "创建日志导出任务")
    public R<AuditExportService.View> create(
            @RequestBody AuditExportService.Command command, HttpServletRequest request) {
        var value = service.create(command, AuditContextDto.from(request));
        worker.ifAvailable(AuditMaintenanceWorker::wake);
        return R.success(value);
    }

    @GetMapping
    @Operation(summary = "查询最近的日志导出任务")
    public R<List<AuditExportService.View>> list() {
        return R.success(service.list());
    }

    @GetMapping("/{id}")
    @Operation(summary = "查询日志导出任务详情")
    public R<AuditExportService.View> detail(@PathVariable String id) {
        return R.success(service.detail(id));
    }

    @PostMapping("/{id}/cancel")
    @Operation(summary = "取消日志导出任务")
    public R<AuditExportService.View> cancel(@PathVariable String id, HttpServletRequest request) {
        return R.success(service.cancel(id, AuditContextDto.from(request)));
    }

    @DeleteMapping("/{id}/file")
    @Operation(summary = "删除导出文件，保留原始日志")
    public R<AuditExportService.View> remove(@PathVariable String id, HttpServletRequest request) {
        return R.success(service.remove(id, AuditContextDto.from(request)));
    }

    @GetMapping("/{id}/download")
    @Operation(summary = "下载已完成的日志导出文件")
    public void download(
            @PathVariable String id, HttpServletRequest request, HttpServletResponse response)
            throws IOException {
        service.download(id, response, AuditContextDto.from(request));
    }
}
