package com.streamfusion.platform.monitor.security;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

@RestController
@RequiredArgsConstructor
@RequestMapping("/audit/security-alerts")
@ModuleAccess("audit")
@Tag(name = "安全告警")
public class SecurityAlertController {
    private final SecurityMonitor monitor;
    private final CurrentUserService current;
    private final AccessService access;

    @GetMapping("/page")
    @Operation(summary = "超级管理员查看本机安全告警", description = "最近7天，最多1000条；规则提示待核查，不代表攻击已成功。")
    public R<SecurityMonitor.Page> page(
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "20") int size) {
        requireAccess(current.requireNormal().getId());
        if (page < 1 || page > 1000 || size < 1 || size > 100)
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        return R.success(monitor.page(page, size));
    }

    @GetMapping("/feed")
    @Operation(summary = "只读检查安全告警摘要", description = "正常运行由会话状态过滤器处理；不续期登录会话。需要有效audit页面和超级管理员身份。")
    public R<SecurityMonitor.Feed> feed() {
        // Defense in depth if the servlet filter is absent (e.g. isolated MVC tests).
        requireAccess(current.requireNormal().getId());
        return R.success(monitor.feed());
    }

    private void requireAccess(long actor) {
        if (!access.isSuperAdmin(actor) || !access.hasModuleAccess(actor, "audit"))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
    }
}
