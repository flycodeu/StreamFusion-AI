package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.pojo.dto.CameraCreateDto;
import com.streamfusion.platform.camera.pojo.dto.CameraProfileCreateDto;
import com.streamfusion.platform.camera.pojo.dto.CameraProfileUpdateDto;
import com.streamfusion.platform.camera.pojo.dto.CameraQueryDto;
import com.streamfusion.platform.camera.pojo.dto.CameraUpdateDto;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceGroupVo;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceMovePreviewVo;
import com.streamfusion.platform.camera.pojo.vo.CameraImpactVo;
import com.streamfusion.platform.camera.pojo.vo.CameraProfileVo;
import com.streamfusion.platform.camera.pojo.vo.CameraVo;
import com.streamfusion.platform.camera.service.CameraAssetService;
import com.streamfusion.platform.camera.service.CameraGroupService;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.media.Schema;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.util.List;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

@Tag(name = "相机管理")
@RestController
@RequiredArgsConstructor
@RequestMapping("/cameras")
@ModuleAccess("camera")
public class CameraController {
    private final CameraAssetService service;
    private final CameraGroupService groups;

    @GetMapping("/options")
    @Operation(summary = "读取当前相机管理操作资格")
    public R<Map<String, Boolean>> options() {
        return R.success(service.options());
    }

    @GetMapping("/page")
    @Operation(summary = "分页查询当前范围内的相机")
    public R<PageResultVo<CameraVo>> page(@ParameterObject @ModelAttribute CameraQueryDto query) {
        return R.success(service.page(query));
    }

    @GetMapping("/devices/page")
    @Operation(summary = "按设备分页展示当前授权范围内的通道集合")
    public R<PageResultVo<CameraDeviceGroupVo>> devices(
            @ParameterObject @ModelAttribute CameraQueryDto query) {
        return R.success(service.deviceGroups(query));
    }

    @GetMapping("/devices/{groupKey}/channels")
    @Operation(summary = "分页查询设备内当前授权范围的通道")
    public R<PageResultVo<CameraVo>> deviceChannels(
            @PathVariable String groupKey, @ParameterObject @ModelAttribute CameraQueryDto query) {
        return R.success(service.deviceChannels(groupKey, query));
    }

    @Schema(description = "整台设备全部通道的移组请求")
    public record DeviceMoveDto(
            @Schema(description = "目标视频分组ID") String targetGroupId,
            @Schema(description = "预览返回的确认凭据；提交时必填") String confirmation) {}

    @PostMapping("/devices/{groupKey}/move-preview")
    @Operation(summary = "预览设备全部通道移组的授权影响")
    public R<CameraDeviceMovePreviewVo> deviceMovePreview(
            @PathVariable String groupKey, @RequestBody DeviceMoveDto input) {
        return R.success(groups.deviceMovePreview(groupKey, input.targetGroupId()));
    }

    @PostMapping("/devices/{groupKey}/move")
    @Operation(summary = "在一个事务中移动设备全部通道")
    public R<Void> deviceMove(
            @PathVariable String groupKey,
            @RequestBody DeviceMoveDto input,
            HttpServletRequest request) {
        groups.moveDevice(
                groupKey,
                input.targetGroupId(),
                input.confirmation(),
                AuditContextDto.from(request));
        return R.success();
    }

    @GetMapping("/{cameraId}")
    @Operation(summary = "查看相机资料和码流档案")
    public R<CameraVo> detail(@PathVariable String cameraId, HttpServletResponse response) {
        return cameraResponse(service.detail(cameraId), response);
    }

    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(summary = "手工登记待归档相机；设备连接保存不联网，码流可以为空")
    public R<Map<String, Object>> create(
            @RequestBody CameraCreateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        Map<String, Object> result = service.create(input, AuditContextDto.from(request));
        response.setHeader(
                HttpHeaders.LOCATION,
                request.getContextPath() + "/cameras/" + result.get("cameraId"));
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote((String) result.get("version")));
        return R.success(result);
    }

    @PutMapping("/{cameraId}")
    @Operation(summary = "修改相机资料、默认码流或已确认的归档状态")
    public R<CameraVo> update(
            @PathVariable String cameraId,
            @RequestBody CameraUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return cameraResponse(
                service.update(cameraId, input, AuditContextDto.from(request)), response);
    }

    @Schema(description = "预览已归档相机启停所影响的授权")
    public record LifecyclePreviewDto(
            @Schema(description = "当前相机编辑版本", requiredMode = Schema.RequiredMode.REQUIRED)
                    String cameraVersion,
            @Schema(
                            description = "目标生命周期",
                            allowableValues = {"ENABLED", "DISABLED"},
                            requiredMode = Schema.RequiredMode.REQUIRED)
                    String targetLifecycle) {}

    @PostMapping("/{cameraId}/lifecycle-preview")
    @Operation(summary = "确认已归档相机启停的授权影响")
    public R<CameraImpactVo> lifecyclePreview(
            @PathVariable String cameraId, @RequestBody LifecyclePreviewDto input) {
        if (input == null)
            throw com.streamfusion.platform.camera.service.CameraAssetRules.invalid("preview");
        return R.success(
                groups.cameraLifecyclePreview(
                        cameraId, input.cameraVersion(), input.targetLifecycle()));
    }

    @DeleteMapping("/{cameraId}")
    @Operation(summary = "删除无外部引用的相机及其码流档案")
    public R<Void> delete(
            @PathVariable String cameraId,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletRequest request) {
        service.delete(cameraId, VersionHeader.require(ifMatch), AuditContextDto.from(request));
        return R.success();
    }

    @GetMapping("/{cameraId}/profiles")
    @Operation(summary = "查询相机码流档案")
    public R<List<CameraProfileVo>> profiles(@PathVariable String cameraId) {
        return R.success(service.profiles(cameraId));
    }

    @PostMapping("/{cameraId}/profiles")
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(summary = "为手工相机新增RTSP码流")
    public R<Map<String, Object>> addProfile(
            @PathVariable String cameraId,
            @RequestBody CameraProfileCreateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        Map<String, Object> result =
                service.addProfile(cameraId, input, AuditContextDto.from(request));
        response.setHeader(
                HttpHeaders.LOCATION,
                request.getContextPath()
                        + "/cameras/"
                        + cameraId
                        + "/profiles/"
                        + result.get("streamProfileId"));
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote((String) result.get("version")));
        return R.success(result);
    }

    @PutMapping("/{cameraId}/profiles/{streamProfileId}")
    @Operation(summary = "修改码流标签、用途、启停或手工定位配置")
    public R<CameraProfileVo> updateProfile(
            @PathVariable String cameraId,
            @PathVariable String streamProfileId,
            @RequestBody CameraProfileUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        CameraProfileVo result =
                service.updateProfile(
                        cameraId, streamProfileId, input, AuditContextDto.from(request));
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(result.version()));
        return R.success(result);
    }

    @DeleteMapping("/{cameraId}/profiles/{streamProfileId}")
    @Operation(summary = "删除未被选为默认值的码流档案")
    public R<Void> deleteProfile(
            @PathVariable String cameraId,
            @PathVariable String streamProfileId,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletRequest request) {
        service.deleteProfile(
                cameraId,
                streamProfileId,
                VersionHeader.require(ifMatch),
                AuditContextDto.from(request));
        return R.success();
    }

    private static R<CameraVo> cameraResponse(CameraVo value, HttpServletResponse response) {
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(value.version()));
        return R.success(value);
    }
}
