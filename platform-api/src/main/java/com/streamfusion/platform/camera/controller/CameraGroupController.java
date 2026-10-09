package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.pojo.dto.CameraGroupWriteDto;
import com.streamfusion.platform.camera.pojo.vo.*;
import com.streamfusion.platform.camera.service.CameraGroupService;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.media.Schema;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.*;
import lombok.RequiredArgsConstructor;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;

@RestController
@RequiredArgsConstructor
@ModuleAccess("camera")
@Tag(name = "视频分组")
public class CameraGroupController {
    private final CameraGroupService groups;

    @Schema(description = "视频组移组影响预览请求")
    public record GroupMove(
            @Schema(description = "当前视频组编辑版本") String version,
            @Schema(description = "目标上级视频组ID，null表示根组") String targetParentId) {}

    @Schema(description = "相机归档或移组影响预览请求")
    public record CameraMove(
            @Schema(description = "当前相机编辑版本") String cameraVersion,
            @Schema(description = "目标视频组ID") String targetGroupId) {}

    @Operation(summary = "分页查询可见视频分组")
    @GetMapping("/camera-groups")
    public R<PageResultVo<CameraGroupVo>> page(
            @RequestParam(required = false) String parentId,
            @RequestParam(required = false) String name,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "100") int size) {
        return R.success(groups.page(parentId, name, page, size));
    }

    @Operation(summary = "查询可见分组及必要祖先，最多1000组")
    @GetMapping("/camera-groups/tree")
    public R<java.util.List<CameraGroupVo>> tree() {
        return R.success(groups.visibleTree());
    }

    @Operation(summary = "查询视频分组详情")
    @GetMapping("/camera-groups/{id}")
    public R<CameraGroupVo> get(@PathVariable String id, HttpServletResponse response) {
        return etag(groups.get(id), response);
    }

    @Operation(summary = "创建视频分组")
    @PostMapping("/camera-groups")
    @ResponseStatus(HttpStatus.CREATED)
    public R<CameraGroupVo> create(
            @RequestBody CameraGroupWriteDto body,
            HttpServletRequest request,
            HttpServletResponse response) {
        var value = groups.create(body, AuditContextDto.from(request));
        response.setHeader(
                HttpHeaders.LOCATION,
                request.getContextPath() + "/camera-groups/" + value.groupId());
        return etag(value, response);
    }

    @Operation(summary = "编辑视频分组或确认移组")
    @PutMapping("/camera-groups/{id}")
    public R<CameraGroupVo> update(
            @PathVariable String id,
            @RequestBody CameraGroupWriteDto body,
            HttpServletRequest request,
            HttpServletResponse response) {
        return etag(groups.update(id, body, AuditContextDto.from(request)), response);
    }

    @Operation(summary = "删除无引用的视频分组")
    @DeleteMapping("/camera-groups/{id}")
    public R<Void> delete(
            @PathVariable String id,
            @RequestHeader(value = "If-Match", required = false) String version,
            HttpServletRequest request) {
        groups.delete(id, VersionHeader.require(version), AuditContextDto.from(request));
        return R.success();
    }

    @Operation(summary = "预览视频组移组的授权影响")
    @PostMapping("/camera-groups/{id}/move-preview")
    public R<CameraImpactVo> move(@PathVariable String id, @RequestBody GroupMove body) {
        return R.success(groups.movePreview(id, body.version(), body.targetParentId()));
    }

    @Operation(summary = "预览相机归档或移组的授权影响")
    @PostMapping("/cameras/{cameraId}/move-preview")
    public R<CameraImpactVo> cameraMove(
            @PathVariable String cameraId, @RequestBody CameraMove body) {
        return R.success(
                groups.cameraMovePreview(cameraId, body.cameraVersion(), body.targetGroupId()));
    }

    private R<CameraGroupVo> etag(CameraGroupVo value, HttpServletResponse response) {
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(value.version()));
        return R.success(value);
    }
}
