package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.access.pojo.*;
import com.streamfusion.platform.camera.access.service.*;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.media.Schema;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.util.List;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

@RestController
@RequiredArgsConstructor
@RequestMapping("/camera-access")
@ModuleAccess("camera")
@Tag(name = "相机接入")
public class CameraAccessController {
    private final CameraAccessJobsService jobs;
    private final CameraAccessWorker worker;
    private final CameraBulkImportService bulk;

    @Schema(description = "整批平台目录导入的范围与一次授权确认")
    public record BulkImportRequest(
            @Schema(description = "首次读取目录任务的当前版本") String version,
            @Schema(description = "新相机目标视频组；空值表示待归档") String groupId,
            @Schema(description = "影响预览返回的五分钟确认令牌") String confirmation,
            @Schema(description = "带时间前缀的幂等请求键；未知结果须使用原键重试") String clientRequestId) {}

    @Schema(description = "连接设备并获取有界通道目录")
    public record ConnectRequest(String clientRequestId, CameraConnection connection) {
        @Override
        public String toString() {
            return "ConnectRequest[redacted]";
        }
    }

    @Schema(description = "确认选择的发现通道与码流")
    public record ImportRequest(
            String version,
            List<ImportSelection.Selection> selections,
            String groupId,
            String confirmation,
            String clientRequestId,
            @Schema(description = "显式绑定既有手工相机档案的ID，仅允许单个候选") String targetCameraId,
            @Schema(description = "待绑定相机档案的当前版本") String targetCameraVersion) {
        ImportSelection selection() {
            return new ImportSelection(
                    selections, groupId, confirmation, targetCameraId, targetCameraVersion);
        }
    }

    @PostMapping("/scan-jobs")
    @ResponseStatus(HttpStatus.ACCEPTED)
    @Operation(summary = "提交有界IPv4端口候选探测，不识别或创建相机")
    public R<Map<String, Object>> scan(
            @RequestBody CameraScanRequest input,
            HttpServletRequest request,
            HttpServletResponse response) {
        Map<String, Object> result = jobs.createScan(input, session(request));
        String id = result.get("jobId").toString();
        if ("QUEUED".equals(result.get("status"))) worker.submit(Long.parseLong(id));
        response.setHeader("Location", request.getContextPath() + "/camera-access/jobs/" + id);
        return R.success(result);
    }

    @GetMapping("/options")
    @Operation(summary = "读取可用接入方式与服务准备状态")
    public R<Map<String, Object>> options() {
        return R.success(jobs.options());
    }

    @PostMapping("/jobs")
    @ResponseStatus(HttpStatus.ACCEPTED)
    @Operation(summary = "提交设备发现或RTSP地址整理任务")
    public R<Map<String, Object>> create(
            @RequestBody ConnectRequest input,
            HttpServletRequest request,
            HttpServletResponse response) {
        if (input == null || input.connection() == null) throw CameraSourceRules.invalid();
        Map<String, Object> result =
                jobs.create(input.clientRequestId(), input.connection(), session(request));
        String id = result.get("jobId").toString();
        if ("QUEUED".equals(result.get("status"))) worker.submit(Long.parseLong(id));
        response.setHeader("Location", request.getContextPath() + "/camera-access/jobs/" + id);
        return R.success(result);
    }

    @GetMapping("/jobs/{id}")
    @Operation(summary = "查看当前会话设备发现结果")
    public R<Map<String, Object>> read(@PathVariable String id, HttpServletRequest request) {
        return R.success(jobs.read(id, session(request)));
    }

    @GetMapping("/jobs")
    @Operation(summary = "分页恢复当前登录会话本人的后台整批导入任务")
    public R<PageResultVo<Map<String, Object>>> list(
            @RequestParam(defaultValue = "BULK_IMPORT") String kind,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "20") int size,
            HttpServletRequest request) {
        return R.success(bulk.list(kind, page, size, session(request)));
    }

    @GetMapping("/jobs/{id}/import-items")
    @Operation(summary = "分页查询整批导入逐项结果；任务凭据清除后结果仍按当前权限保护")
    public R<PageResultVo<Map<String, Object>>> items(
            @PathVariable String id,
            @RequestParam(required = false) String status,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "100") int size,
            HttpServletRequest request) {
        return R.success(bulk.items(id, status, page, size, session(request)));
    }

    @PostMapping("/jobs/{id}/bulk-import-preview")
    @Operation(summary = "预览整批平台目录导入范围与新相机分组授权影响")
    public R<Map<String, Object>> bulkPreview(
            @PathVariable String id,
            @RequestBody BulkImportRequest input,
            HttpServletRequest request) {
        if (input == null || input.version() == null) throw CameraSourceRules.invalid();
        return R.success(bulk.preview(id, input.version(), input.groupId(), session(request)));
    }

    @PostMapping("/jobs/{id}/bulk-import")
    @ResponseStatus(HttpStatus.ACCEPTED)
    @Operation(summary = "一次确认后后台分页导入平台目录；已提交相机不随取消回滚")
    public R<Map<String, Object>> bulkStart(
            @PathVariable String id,
            @RequestBody BulkImportRequest input,
            HttpServletRequest request,
            HttpServletResponse response) {
        if (input == null || input.version() == null) throw CameraSourceRules.invalid();
        var result =
                bulk.start(
                        id,
                        input.version(),
                        input.groupId(),
                        input.confirmation(),
                        input.clientRequestId(),
                        session(request),
                        AuditContextDto.from(request));
        if ("QUEUED".equals(result.get("status"))) worker.submit(Long.parseLong(id));
        response.setHeader("Location", request.getContextPath() + "/camera-access/jobs/" + id);
        return R.success(result);
    }

    @PostMapping("/jobs/{id}/cancel")
    @Operation(summary = "取消当前会话设备发现并清除暂存凭据")
    public R<Map<String, Object>> cancel(@PathVariable String id, HttpServletRequest request) {
        return R.success(jobs.cancel(id, session(request)));
    }

    @PostMapping("/jobs/{id}/import-preview")
    @Operation(summary = "预览通道导入与分组授权影响")
    public R<Map<String, Object>> preview(
            @PathVariable String id, @RequestBody ImportRequest input, HttpServletRequest request) {
        validate(input);
        return R.success(jobs.preview(id, input.version(), input.selection(), session(request)));
    }

    @PostMapping("/jobs/{id}/import")
    @Operation(summary = "按确认结果原子导入所选通道和码流")
    public R<Map<String, Object>> importSelected(
            @PathVariable String id, @RequestBody ImportRequest input, HttpServletRequest request) {
        validate(input);
        return R.success(
                jobs.importSelected(
                        id,
                        input.version(),
                        input.selection(),
                        input.clientRequestId(),
                        session(request),
                        AuditContextDto.from(request)));
    }

    private static void validate(ImportRequest input) {
        if (input == null
                || input.version() == null
                || input.selections() == null
                || input.selections().isEmpty()
                || input.selections().size() > 256) throw CameraSourceRules.invalid();
    }

    private static String session(HttpServletRequest request) {
        if (request.getSession(false) == null) throw CameraSourceRules.invalid();
        return request.getSession(false).getId();
    }
}
