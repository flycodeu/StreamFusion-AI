package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.media.Schema;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/camera-status/plans")
@ModuleAccess("camera")
@Tag(name = "相机巡检任务")
@RequiredArgsConstructor
public class CameraStatusPlanController {
    private final CameraStatusPlanService service;
    private final CameraStatusPlanTargetsService targets;

    @PostMapping("/targets/preview")
    @Operation(summary = "核对巡检范围内可检测的相机", description = "只核对接入配置和授权，不连接相机；支持全量、指定设备及已有任务范围。")
    public R<CameraStatusPlanTargetsService.Preview> preview(
            @RequestBody CameraStatusPlanTargetsService.Query query) {
        return R.success(targets.preview(query));
    }

    @GetMapping
    @Operation(summary = "查询当前账号的巡检任务")
    public R<List<CameraStatusPlanVo>> list() {
        return R.success(service.list());
    }

    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(
            summary = "创建并启动定时巡检任务",
            description = "每台设备选择一路代表通道，错峰检查；登出后继续执行，到期停止。相同请求编号和配置不会重复创建。")
    public R<CameraStatusPlanVo> create(
            @RequestBody CameraStatusPlanCommand command, HttpServletRequest request) {
        return R.success(service.create(command, AuditContextDto.from(request)));
    }

    public record StopCommand(@Schema(description = "任务当前版本，用于并发校验") String version) {}

    @PostMapping("/{id}/stop")
    @Operation(summary = "停止指定巡检任务")
    public R<CameraStatusPlanVo> stop(
            @PathVariable String id, @RequestBody StopCommand command, HttpServletRequest request) {
        return R.success(
                service.stop(
                        id,
                        command == null ? null : command.version(),
                        AuditContextDto.from(request)));
    }
}
