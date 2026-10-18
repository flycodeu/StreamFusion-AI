package com.streamfusion.platform.system.access.query;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.http.HttpHeaders;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.ModelAttribute;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequiredArgsConstructor
@RequestMapping("/access-query")
@ModuleAccess("access-query")
@Tag(name = "只读权限查询")
public class AccessQueryController {
    private final AccessQueryService service;

    @GetMapping("/users/page")
    @Operation(summary = "分页搜索最小用户身份")
    public R<AccessQueryViews.Result<AccessQueryViews.UserIdentity>> users(
            @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.users(query));
    }

    @GetMapping("/pages/page")
    @Operation(summary = "分页搜索PAGE权限对象")
    public R<AccessQueryViews.Result<AccessQueryViews.PageInfo>> pages(
            @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.pages(query));
    }

    @GetMapping("/roles/page")
    @Operation(summary = "分页查看角色、关联用户数与页面数")
    public R<AccessQueryViews.Result<AccessQueryViews.RoleOverview>> roles(
            @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.roles(query));
    }

    @GetMapping("/roles/{roleId}/pages")
    @Operation(summary = "查看角色当前获授页面及生效原因")
    public R<AccessQueryViews.Result<AccessQueryViews.RolePage>> rolePages(
            @PathVariable String roleId, @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.rolePages(roleId, query));
    }

    @GetMapping("/roles/{roleId}/users")
    @Operation(summary = "查看角色关联账号")
    public R<AccessQueryViews.Result<AccessQueryViews.UserIdentity>> roleUsers(
            @PathVariable String roleId, @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.roleUsers(roleId, query));
    }

    @GetMapping("/pages/overview")
    @Operation(summary = "分页查看页面授予角色数与去重账号数")
    public R<AccessQueryViews.Result<AccessQueryViews.PageOverview>> pageOverview(
            @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.pageOverview(query));
    }

    @GetMapping("/pages/export.xlsx")
    @Operation(summary = "导出匹配PAGE及其当前具备资格的去重账号和部门")
    public ResponseEntity<byte[]> exportPages(@RequestParam(required = false) String keyword) {
        var file = service.exportPages(keyword);
        return ResponseEntity.ok()
                .contentType(
                        MediaType.parseMediaType(
                                "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"))
                .header(
                        HttpHeaders.CONTENT_DISPOSITION,
                        "attachment; filename=\"" + file.filename() + "\"")
                .header("X-Content-Type-Options", "nosniff")
                .body(file.bytes());
    }

    @GetMapping("/users/{userId}/pages")
    @Operation(summary = "查询用户当前PAGE资格及授予角色摘要")
    public R<AccessQueryViews.Result<AccessQueryViews.UserPage>> userPages(
            @PathVariable String userId, @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.userPages(userId, query));
    }

    @GetMapping("/pages/{pageId}/users")
    @Operation(summary = "反查当前被授予PAGE的用户，不代表实际访问记录")
    public R<AccessQueryViews.Result<AccessQueryViews.PageUser>> pageUsers(
            @PathVariable String pageId, @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.pageUsers(pageId, query));
    }

    @GetMapping("/pages/{pageId}/roles")
    @Operation(summary = "查询PAGE授予角色及当前用户来源明细")
    public R<AccessQueryViews.Result<AccessQueryViews.PageRole>> pageRoles(
            @PathVariable String pageId, @ParameterObject @ModelAttribute AccessQueryDto query) {
        return R.success(service.pageRoles(pageId, query));
    }
}
