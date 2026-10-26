package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.camera.ingress.CameraIngressService;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

@RestController
@RequiredArgsConstructor
@ModuleAccess("camera")
@RequestMapping("/camera-access/ingress")
public class CameraIngressController {
    private final CameraIngressService ingress;
    private final CameraAccessService access;

    @GetMapping
    public R<Map<String, Object>> status() {
        access.requireSuper(access.readActor());
        return R.success(ingress.status());
    }
}
