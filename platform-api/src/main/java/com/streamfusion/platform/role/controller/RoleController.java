package com.streamfusion.platform.role.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import com.streamfusion.platform.role.pojo.dto.RoleCreateDto;
import com.streamfusion.platform.role.pojo.dto.RoleMenusUpdateDto;
import com.streamfusion.platform.role.pojo.dto.RoleQueryDto;
import com.streamfusion.platform.role.pojo.dto.RoleUpdateDto;
import com.streamfusion.platform.role.pojo.dto.RoleVersionDto;
import com.streamfusion.platform.role.pojo.vo.RoleDetailVo;
import com.streamfusion.platform.role.pojo.vo.RoleMenusVo;
import com.streamfusion.platform.role.pojo.vo.RoleOptionVo;
import com.streamfusion.platform.role.service.RoleService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

@Tag(name = "角色管理")
@RestController
@RequiredArgsConstructor
@RequestMapping("/roles")
@ModuleAccess("role")
public class RoleController {
    private final RoleService service;

    @Operation(summary = "分页查询角色")
    @GetMapping
    public R<PageResultVo<RoleDetailVo>> page(@ParameterObject @ModelAttribute RoleQueryDto query) {
        return R.success(service.page(query));
    }

    @Operation(summary = "查询角色选择项")
    @GetMapping("/options")
    public R<List<RoleOptionVo>> options() {
        return R.success(service.options());
    }

    @Operation(summary = "创建角色")
    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    public R<RoleDetailVo> create(
            @RequestBody RoleCreateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        RoleDetailVo role = service.create(input, AuditContextDto.from(request));
        response.setHeader(HttpHeaders.LOCATION, request.getContextPath() + "/roles/" + role.id());
        return roleResponse(role, response);
    }

    @Operation(summary = "修改角色")
    @PutMapping("/{id}")
    public R<RoleDetailVo> update(
            @PathVariable String id,
            @RequestBody RoleUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return roleResponse(service.update(id, input, AuditContextDto.from(request)), response);
    }

    @Operation(summary = "启用角色")
    @PostMapping("/{id}/enable")
    public R<RoleDetailVo> enable(
            @PathVariable String id,
            @RequestBody RoleVersionDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return roleResponse(
                service.changeStatus(id, input.getVersion(), true, AuditContextDto.from(request)),
                response);
    }

    @Operation(summary = "禁用角色")
    @PostMapping("/{id}/disable")
    public R<RoleDetailVo> disable(
            @PathVariable String id,
            @RequestBody RoleVersionDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return roleResponse(
                service.changeStatus(id, input.getVersion(), false, AuditContextDto.from(request)),
                response);
    }

    @Operation(summary = "删除角色")
    @DeleteMapping("/{id}")
    public R<Void> delete(
            @PathVariable String id,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletRequest request) {
        service.delete(id, VersionHeader.require(ifMatch), AuditContextDto.from(request));
        return R.success();
    }

    @Operation(summary = "查询角色菜单")
    @GetMapping("/{id}/menus")
    public R<RoleMenusVo> menus(@PathVariable String id) {
        return R.success(service.menus(id));
    }

    @Operation(summary = "设置角色菜单")
    @PutMapping("/{id}/menus")
    public R<RoleMenusVo> updateMenus(
            @PathVariable String id,
            @RequestBody RoleMenusUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        RoleMenusVo result = service.updateMenus(id, input, AuditContextDto.from(request));
        response.setHeader(HttpHeaders.ETAG, "\"" + result.version() + "\"");
        return R.success(result);
    }

    private static R<RoleDetailVo> roleResponse(RoleDetailVo role, HttpServletResponse response) {
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(role.version()));
        return R.success(role);
    }
}
