package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springdoc.core.annotations.ParameterObject;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.ModelAttribute;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequiredArgsConstructor
@Tag(name = "相机状态")
@ModuleAccess("camera")
public class CameraStatusPageController {
    private final CameraStatusQueryService service;

    @GetMapping("/camera-status")
    @Operation(summary = "按当前授权通道与所选层分页读取状态，不触发网络或媒体检查")
    public R<CameraStatusPageVo> page(
            @ParameterObject @ModelAttribute CameraStatusPageQuery query) {
        return R.success(service.page(query));
    }

    @GetMapping("/camera-status/devices")
    @Operation(summary = "按当前授权设备分页读取代表通道状态；每设备仅选一路，不推断所有通道健康")
    public R<CameraStatusPageVo> devices(
            @ParameterObject @ModelAttribute CameraStatusPageQuery query) {
        return R.success(service.devicePage(query));
    }
}
