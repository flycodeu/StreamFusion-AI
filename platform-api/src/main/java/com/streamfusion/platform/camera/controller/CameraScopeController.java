package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.pojo.dto.CameraScopeUpdateDto;
import com.streamfusion.platform.camera.pojo.vo.*;
import com.streamfusion.platform.camera.service.*;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.*;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/camera-scopes")
@RequiredArgsConstructor
@ModuleAccess("camera_scope")
@Tag(name = "账户相机授权")
public class CameraScopeController {
    private final CameraScopeService scopes;
    private final CameraGroupService groups;
    private final CameraAccessService access;

    @Operation(summary = "分页查询相机授权目标账户")
    @GetMapping("/user-options")
    public R<PageResultVo<Map<String, Object>>> users(
            @RequestParam(required = false) String name,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "100") int size) {
        return R.success(scopes.userOptions(name, page, size));
    }

    @Operation(summary = "分页查询可授权视频分组")
    @GetMapping("/group-options")
    public R<PageResultVo<CameraGroupVo>> groups(
            @RequestParam(required = false) String parentId,
            @RequestParam(required = false) String name,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "100") int size) {
        access.requireSuper(access.readActor());
        return R.success(groups.page(parentId, name, page, size));
    }

    @Operation(summary = "分页查询可新增授权的启用相机")
    @GetMapping("/camera-options/page")
    public R<PageResultVo<Map<String, Object>>> cameras(
            @RequestParam(required = false) String groupId,
            @RequestParam(defaultValue = "false") boolean includeDescendants,
            @RequestParam(required = false) String name,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "100") int size) {
        return R.success(scopes.cameraOptions(groupId, includeDescendants, name, page, size));
    }

    @Operation(summary = "查询账户相机数据范围")
    @GetMapping("/users/{userId}")
    public R<CameraScopeVo> get(@PathVariable String userId, HttpServletResponse response) {
        return etag(scopes.get(userId), response);
    }

    @Operation(summary = "替换账户相机数据授权")
    @PutMapping("/users/{userId}")
    public R<CameraScopeVo> put(
            @PathVariable String userId,
            @RequestBody CameraScopeUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return etag(scopes.update(userId, input, AuditContextDto.from(request)), response);
    }

    @Operation(summary = "查询账户拥有相机范围的授权来源")
    @GetMapping("/users/{userId}/cameras/{cameraId}/reasons")
    public R<Map<String, Object>> reasons(
            @PathVariable String userId, @PathVariable String cameraId) {
        return R.success(scopes.reasons(userId, cameraId));
    }

    private R<CameraScopeVo> etag(CameraScopeVo value, HttpServletResponse response) {
        response.setHeader("ETag", VersionHeader.quote(value.version()));
        return R.success(value);
    }
}
