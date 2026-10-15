package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestController;

@RestController
@Tag(name = "相机状态")
@RequiredArgsConstructor
@RequestMapping("/cameras/{cameraId}")
@ModuleAccess("camera")
public class CameraStatusController {
    private final CameraStatusService service;
    private final CameraStatusQueryService query;

    @GetMapping("/status")
    @Operation(summary = "读取当前授权通道的码流控制状态，不外呼相机")
    public R<CameraStatusSnapshotVo> status(@PathVariable String cameraId) {
        return R.success(query.status(cameraId));
    }

    @PostMapping("/status-checks")
    @Operation(summary = "手动检查指定RTSP码流，仅DESCRIBE并返回后台执行回执")
    @ResponseStatus(HttpStatus.ACCEPTED)
    public R<CameraStatusCheckVo> create(
            @PathVariable String cameraId,
            @RequestBody CameraStatusCheckCommand command,
            HttpServletRequest request) {
        var session = request.getSession(false);
        if (session == null) throw BusinessException.error(ErrorCode.UNAUTHORIZED);
        var receipt = service.createManual(cameraId, command, session.getId());
        service.dispatch(Long.parseLong(receipt.checkId()), session.getId());
        return R.success(receipt);
    }

    @GetMapping("/status-checks/{checkId}")
    @Operation(summary = "读取当前授权通道的单次检查回执")
    public R<CameraStatusCheckVo> check(
            @PathVariable String cameraId, @PathVariable String checkId) {
        return R.success(query.check(cameraId, checkId));
    }
}
