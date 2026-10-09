package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.pojo.dto.CameraDeviceUpdateDto;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceVo;
import com.streamfusion.platform.camera.service.CameraAssetService;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpHeaders;
import org.springframework.web.bind.annotation.*;

@Tag(name = "相机设备档案")
@RestController
@RequiredArgsConstructor
@RequestMapping("/camera-devices")
@ModuleAccess("camera")
public class CameraDeviceController {
    private final CameraAssetService service;

    @GetMapping("/{deviceId}")
    @Operation(summary = "查看本地设备观测档案")
    public R<CameraDeviceVo> detail(@PathVariable String deviceId, HttpServletResponse response) {
        CameraDeviceVo value = service.device(deviceId);
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(value.version()));
        return R.success(value);
    }

    @PutMapping("/{deviceId}")
    @Operation(summary = "编辑设备本地名称和备注")
    public R<CameraDeviceVo> update(
            @PathVariable String deviceId,
            @RequestBody CameraDeviceUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        CameraDeviceVo value = service.updateDevice(deviceId, input, AuditContextDto.from(request));
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(value.version()));
        return R.success(value);
    }

    @DeleteMapping("/{deviceId}")
    @Operation(summary = "删除无相机引用的本地设备档案")
    public R<Void> delete(
            @PathVariable String deviceId,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletRequest request) {
        service.deleteDevice(
                deviceId, VersionHeader.require(ifMatch), AuditContextDto.from(request));
        return R.success();
    }
}
