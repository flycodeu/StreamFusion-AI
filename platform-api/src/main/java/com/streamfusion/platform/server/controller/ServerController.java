package com.streamfusion.platform.server.controller;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import com.streamfusion.platform.server.service.ServerMonitorService;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import java.time.Instant;
import java.util.concurrent.CompletableFuture;
import lombok.RequiredArgsConstructor;
import org.slf4j.MDC;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequiredArgsConstructor
@RequestMapping("/server")
@ModuleAccess("server")
@Tag(name = "服务信息")
public class ServerController {
    private final ServerMonitorService service;

    @GetMapping("/status")
    @Operation(summary = "查询当前主机与项目服务状态")
    public CompletableFuture<R<ServerSnapshotVo>> status() {
        String traceId = MDC.get("traceId");
        return service.snapshot()
                .thenApply(
                        snapshot -> new R<>("SUCCESS", "操作成功", snapshot, traceId, Instant.now()));
    }
}
