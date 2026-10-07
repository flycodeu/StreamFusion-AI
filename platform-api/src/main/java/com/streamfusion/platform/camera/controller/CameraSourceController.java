package com.streamfusion.platform.camera.controller;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.camera.pojo.dto.*;
import com.streamfusion.platform.camera.service.CameraSourceService;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.*;

@Tag(name = "相机接入源")
@RestController
@RequiredArgsConstructor
@RequestMapping("/camera-sources")
@ModuleAccess("camera")
public class CameraSourceController {
    private final CameraSourceService service;

    @Operation(summary = "读取已开放接入选项与配置就绪状态")
    @GetMapping("/options")
    public R<Map<String, Object>> options() {
        return R.success(service.options());
    }

    @Operation(summary = "分页读取接入来源")
    @GetMapping
    public R<PageResultVo<Map<String, Object>>> page(@ModelAttribute CameraSourceQueryDto query) {
        return R.success(service.page(query));
    }

    @Operation(summary = "读取脱敏接入来源配置")
    @GetMapping("/{id}")
    public R<Map<String, Object>> detail(@PathVariable String id, HttpServletResponse response) {
        return tagged(service.detail(id), response);
    }

    @Operation(summary = "保存设备、平台或RTSP接入来源，不发起网络请求")
    @PostMapping
    @ResponseStatus(HttpStatus.CREATED)
    public R<Map<String, Object>> create(
            @RequestBody CameraSourceWriteDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        var result = service.create(input, AuditContextDto.from(request));
        response.setHeader(
                "Location", request.getContextPath() + "/camera-sources/" + result.get("sourceId"));
        return tagged(result, response);
    }

    @Operation(summary = "按版本修改接入来源配置")
    @PutMapping("/{id}")
    public R<Map<String, Object>> update(
            @PathVariable String id,
            @RequestBody CameraSourceUpdateDto input,
            HttpServletRequest request,
            HttpServletResponse response) {
        return tagged(service.update(id, input, AuditContextDto.from(request)), response);
    }

    @Operation(summary = "删除无资产引用的接入来源")
    @DeleteMapping("/{id}")
    public R<Void> delete(
            @PathVariable String id,
            @RequestHeader(name = "If-Match", required = false) String version,
            HttpServletRequest request) {
        service.delete(id, VersionHeader.require(version), AuditContextDto.from(request));
        return R.success();
    }

    private static R<Map<String, Object>> tagged(
            Map<String, Object> data, HttpServletResponse response) {
        response.setHeader("ETag", VersionHeader.quote(data.get("version").toString()));
        return R.success(data);
    }
}
