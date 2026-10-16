package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.camera.access.service.CameraPlatformDirectoryService;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/camera-access/platform-regions")
@ModuleAccess("camera")
@Tag(name = "海康平台目录")
@RequiredArgsConstructor
public class CameraPlatformDirectoryController {
    private final CameraPlatformDirectoryService service;

    @PostMapping
    @Operation(summary = "按父节点分页读取区域；未指定父节点时只读取根区域")
    public R<CameraPlatformDirectoryService.Regions> regions(
            @RequestBody CameraPlatformDirectoryService.Query query) {
        try {
            return R.success(service.regions(query));
        } catch (com.streamfusion.platform.camera.access.adapter.CameraAdapterException ex) {
            throw com.streamfusion.platform.common.exception.BusinessException.error(
                            com.streamfusion.platform.common.exception.ErrorCode.CONFLICT)
                    .diagnosed(ex.reasonCode());
        }
    }
}
