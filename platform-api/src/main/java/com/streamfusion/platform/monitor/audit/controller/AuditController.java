package com.streamfusion.platform.monitor.audit.controller;

import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditQueryDto;
import com.streamfusion.platform.monitor.audit.pojo.vo.AuditDetailVo;
import com.streamfusion.platform.monitor.audit.pojo.vo.AuditEntryVo;
import com.streamfusion.platform.monitor.audit.service.AuditQueryService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.web.bind.annotation.*;

@RestController
@RequiredArgsConstructor
@RequestMapping("/audit")
@ModuleAccess("audit")
@Tag(name = "操作记录")
public class AuditController {
    private final AuditQueryService service;

    @GetMapping("/page")
    @Operation(summary = "分页查询操作记录")
    public R<PageResultVo<AuditEntryVo>> page(
            @ParameterObject @ModelAttribute AuditQueryDto query) {
        return R.success(service.page(query));
    }

    @GetMapping("/{id}")
    @Operation(summary = "查询操作记录详情")
    public R<AuditDetailVo> detail(@PathVariable String id) {
        return R.success(service.detail(id));
    }
}
