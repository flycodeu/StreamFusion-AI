package com.streamfusion.platform.media.runtime.controller;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.common.web.VersionHeader;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Selection;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import jakarta.servlet.http.HttpServletResponse;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpHeaders;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.PutMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequiredArgsConstructor
@RequestMapping("/media-runtime")
@ModuleAccess("server")
@Tag(name = "媒体环境")
public class MediaRuntimeController {
    private final MediaRuntimeService service;

    @GetMapping
    @Operation(summary = "读取本机媒体运行时检测结果")
    public R<MediaRuntimeSnapshot> current(HttpServletResponse response) {
        return result(service.current(), response);
    }

    @PostMapping("/detect")
    @Operation(summary = "重新检测本机媒体运行时")
    public R<MediaRuntimeSnapshot> detect(HttpServletResponse response) {
        return result(service.detect(), response);
    }

    @PutMapping("/selection")
    @Operation(summary = "选择当前操作系统使用的已发现版本")
    public R<MediaRuntimeSnapshot> select(
            @RequestBody Selection body,
            @RequestHeader(name = "If-Match", required = false) String ifMatch,
            HttpServletResponse response) {
        return result(
                service.select(body.zlmId(), body.ffmpegId(), VersionHeader.require(ifMatch)),
                response);
    }

    private R<MediaRuntimeSnapshot> result(
            MediaRuntimeSnapshot snapshot, HttpServletResponse response) {
        response.setHeader(HttpHeaders.ETAG, VersionHeader.quote(snapshot.version()));
        response.setHeader(HttpHeaders.CACHE_CONTROL, "no-store");
        return R.success(snapshot);
    }
}
