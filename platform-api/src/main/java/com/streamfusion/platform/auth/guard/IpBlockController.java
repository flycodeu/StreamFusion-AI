package com.streamfusion.platform.auth.guard;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.auth.guard.pojo.dto.IpBlockQueryDto;
import com.streamfusion.platform.auth.guard.pojo.vo.IpBlockVo;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.web.bind.annotation.*;

@RestController
@RequiredArgsConstructor
@RequestMapping("/audit/ip-blocks")
@ModuleAccess("audit")
@Tag(name = "IP安全封禁")
public class IpBlockController {
    private final IpBlockService service;

    @GetMapping("/page")
    @Operation(summary = "超级管理员分页查询IP封禁")
    public R<PageResultVo<IpBlockVo>> page(@ParameterObject @ModelAttribute IpBlockQueryDto query) {
        return R.success(service.page(query));
    }

    @PutMapping("/{id}/unblock")
    @Operation(summary = "超级管理员解除IP封禁")
    public R<IpBlockVo> unblock(
            @PathVariable String id,
            @RequestHeader(value = "If-Match", required = false) String version,
            HttpServletRequest request,
            HttpServletResponse response) {
        var result =
                service.unblock(id, VersionHeader.require(version), AuditContextDto.from(request));
        response.setHeader("ETag", VersionHeader.quote(result.version()));
        return R.success(result);
    }
}
