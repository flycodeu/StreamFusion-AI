package com.streamfusion.platform.user.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import com.streamfusion.platform.department.pojo.dto.UserDepartmentsUpdateDto;
import com.streamfusion.platform.department.pojo.vo.DepartmentOptionVo;
import com.streamfusion.platform.department.pojo.vo.UserDepartmentsVo;
import com.streamfusion.platform.department.service.DepartmentService;
import com.streamfusion.platform.menu.pojo.vo.MenuRouteVo;
import com.streamfusion.platform.role.pojo.dto.UserRolesUpdateDto;
import com.streamfusion.platform.role.pojo.vo.RoleOptionVo;
import com.streamfusion.platform.role.pojo.vo.UserRolesVo;
import com.streamfusion.platform.role.service.UserRoleService;
import com.streamfusion.platform.user.pojo.dto.UserCreateDto;
import com.streamfusion.platform.user.pojo.dto.UserProfileUpdateDto;
import com.streamfusion.platform.user.pojo.dto.UserQueryDto;
import com.streamfusion.platform.user.pojo.dto.UserVersionDto;
import com.streamfusion.platform.user.pojo.vo.UserSummaryVo;
import com.streamfusion.platform.user.pojo.vo.UserVo;
import com.streamfusion.platform.user.service.UserManagementService;
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

@Tag(name = "用户管理")
@RestController
@RequiredArgsConstructor
@RequestMapping("/user")
@ModuleAccess("user")
public class UserController {
    private final UserManagementService service;
    private final UserRoleService roleService;
    private final DepartmentService departmentService;

    @Operation(summary = "查询部门选择项")
    @GetMapping("/department-options")
    public R<List<DepartmentOptionVo>> departmentOptions() {
        return R.success(departmentService.options());
    }

    @Operation(summary = "查询用户部门")
    @GetMapping("/{id}/departments")
    public R<UserDepartmentsVo> departments(@PathVariable String id) {
        return R.success(departmentService.userDepartments(id));
    }

    @Operation(summary = "设置用户部门")
    @PutMapping("/{id}/departments")
    public R<UserDepartmentsVo> updateDepartments(
            @PathVariable String id,
            @RequestBody UserDepartmentsUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        UserDepartmentsVo result =
                departmentService.assign(id, input, AuditContextDto.from(request));
        response.setHeader(HttpHeaders.ETAG, "\"" + result.version() + "\"");
        return R.success(result);
    }

    @Operation(summary = "查询角色选择项")
    @GetMapping("/role-options")
    public R<List<RoleOptionVo>> roleOptions() {
        return R.success(roleService.options());
    }

    @Operation(summary = "查询用户角色")
    @GetMapping("/{id}/roles")
    public R<UserRolesVo> roles(@PathVariable String id) {
        return R.success(roleService.get(id));
    }

    @Operation(summary = "设置用户角色")
    @PutMapping("/{id}/roles")
    public R<UserRolesVo> updateRoles(
            @PathVariable String id,
            @RequestBody UserRolesUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        UserRolesVo result = roleService.update(id, input, AuditContextDto.from(request));
        response.setHeader(HttpHeaders.ETAG, "\"" + result.version() + "\"");
        return R.success(result);
    }

    @Operation(summary = "查询用户菜单")
    @GetMapping("/{id}/menus")
    public R<List<MenuRouteVo>> userMenus(@PathVariable String id) {
        return R.success(roleService.routes(id));
    }

    @Operation(summary = "分页查询用户")
    @GetMapping("/page")
    public R<PageResultVo<UserSummaryVo>> page(
            @ParameterObject @ModelAttribute UserQueryDto query) {
        return R.success(service.page(query));
    }

    @Operation(summary = "查询用户详情")
    @GetMapping("/{id}")
    public R<UserVo> get(@PathVariable String id, HttpServletResponse response) {
        return userResponse(service.get(id), response);
    }

    @Operation(summary = "创建用户")
    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    public R<UserVo> create(
            @RequestBody UserCreateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        UserVo user = service.create(input, AuditContextDto.from(request));
        response.setHeader(
                HttpHeaders.LOCATION, request.getContextPath() + "/user/" + user.getId());
        return userResponse(user, response);
    }

    @Operation(summary = "修改用户")
    @PutMapping("/{id}")
    public R<UserVo> update(
            @PathVariable String id,
            @RequestBody UserProfileUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return userResponse(service.update(id, input, AuditContextDto.from(request)), response);
    }

    @Operation(summary = "禁用用户")
    @PostMapping("/{id}/disable")
    public R<UserVo> disable(
            @PathVariable String id,
            @RequestBody UserVersionDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return userResponse(
                service.changeStatus(id, input.getVersion(), false, AuditContextDto.from(request)),
                response);
    }

    @Operation(summary = "启用用户")
    @PostMapping("/{id}/enable")
    public R<UserVo> enable(
            @PathVariable String id,
            @RequestBody UserVersionDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return userResponse(
                service.changeStatus(id, input.getVersion(), true, AuditContextDto.from(request)),
                response);
    }

    @Operation(summary = "重置用户密码")
    @PostMapping("/{id}/reset-password")
    public R<Void> resetPassword(
            @PathVariable String id,
            @RequestBody UserVersionDto input,
            HttpServletRequest request) {
        service.resetPassword(id, input.getVersion(), AuditContextDto.from(request));
        return R.success();
    }

    @Operation(summary = "删除用户")
    @DeleteMapping("/{id}")
    public R<Void> delete(
            @PathVariable String id,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletRequest request) {
        service.delete(id, VersionHeader.require(ifMatch), AuditContextDto.from(request));
        return R.success();
    }

    private static R<UserVo> userResponse(UserVo user, HttpServletResponse response) {
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(user.getVersion()));
        return R.success(user);
    }
}
