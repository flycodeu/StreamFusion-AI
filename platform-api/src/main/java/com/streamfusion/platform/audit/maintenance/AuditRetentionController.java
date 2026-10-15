package com.streamfusion.platform.audit.maintenance;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/audit/retention")
@ModuleAccess("audit")
@Tag(name = "操作日志保留策略")
@RequiredArgsConstructor
public class AuditRetentionController {
    private final AuditRetentionService service;
    private final org.springframework.beans.factory.ObjectProvider<AuditMaintenanceWorker> worker;

    @GetMapping
    @Operation(summary = "查询日志保留策略")
    public R<AuditRetentionService.View> get() {
        return R.success(service.get());
    }

    @PutMapping
    @Operation(summary = "修改并启停日志保留策略")
    public R<AuditRetentionService.View> save(
            @RequestBody AuditRetentionService.Command command, HttpServletRequest request) {
        return R.success(service.save(command, AuditContextDto.from(request)));
    }

    @PostMapping("/preview")
    @Operation(summary = "预览候选清理范围")
    public R<AuditRetentionService.Preview> preview(
            @RequestBody AuditRetentionService.Command command) {
        return R.success(service.preview(command));
    }

    public record Run(String version) {}

    @PostMapping("/run")
    @Operation(summary = "请求后台执行一次已启用策略")
    public R<AuditRetentionService.View> run(@RequestBody Run input, HttpServletRequest request) {
        if (input == null)
            throw com.streamfusion.platform.common.exception.BusinessException.error(
                    com.streamfusion.platform.common.exception.ErrorCode.VALIDATION_ERROR);
        var value = service.requestRun(input.version(), AuditContextDto.from(request));
        worker.ifAvailable(AuditMaintenanceWorker::wake);
        return R.success(value);
    }
}
