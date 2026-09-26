package com.streamfusion.platform.server.pojo.vo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.time.Instant;
import java.util.List;

public record ServerSnapshotVo(
        @Schema(description = "最近成功采集时间，尚未成功采集时为空") Instant sampledAt,
        @Schema(description = "是否为过期缓存或不可用快照") boolean stale,
        @Schema(description = "采集状态：READY、UNAVAILABLE") String state,
        @Schema(description = "JDK 所见运行环境资源，容器中可能受配额限制") Host host,
        @Schema(description = "当前 Platform API 的 JVM 信息") Jvm jvm,
        @Schema(description = "明确配置的项目服务及端口状态") List<ProjectService> services,
        @Schema(description = "GPU 采集状态：AVAILABLE、UNAVAILABLE、DISABLED") String gpuStatus,
        @Schema(description = "可读取的本机 NVIDIA GPU 信息") List<Gpu> gpus) {
    public record Host(
            @Schema(description = "操作系统名称") String os,
            @Schema(description = "处理器架构") String architecture,
            @Schema(description = "可用逻辑处理器数量") int processors,
            @Schema(description = "预读并间隔约一秒后读取的运行环境 CPU 近期使用率百分比，无有效样本时为空") Double cpuPercent,
            @Schema(description = "JDK 所见运行环境内存总量，单位字节") Long memoryTotalBytes,
            @Schema(description = "JDK 所见运行环境已用内存，单位字节") Long memoryUsedBytes) {}

    public record Jvm(
            @Schema(description = "Java 运行时版本") String javaVersion,
            @Schema(description = "当前 JVM 进程 ID 的十进制字符串") String pid,
            @Schema(description = "采样窗口内进程 CPU 使用率，按 JDK 可用逻辑处理器数量归一化，不可用时为空") Double cpuPercent,
            @Schema(description = "已用堆内存，单位字节") long heapUsedBytes,
            @Schema(description = "最大堆内存，单位字节") long heapMaxBytes,
            @Schema(description = "已用非堆内存，单位字节") long nonHeapUsedBytes,
            @Schema(description = "JVM 运行时长，单位毫秒") long uptimeMillis,
            @Schema(description = "JVM 启动时间") Instant startedAt) {}

    public record ProjectService(
            @Schema(description = "项目服务唯一键") String key,
            @Schema(description = "项目服务名称") String name,
            @Schema(description = "配置的服务主机，不包含凭据") String host,
            @Schema(description = "配置的服务端口，未配置时为空") Integer port,
            @Schema(description = "服务状态：RUNNING、REACHABLE、UNREACHABLE、NOT_CONFIGURED、UNAVAILABLE")
                    String status) {}

    public record Gpu(
            @Schema(description = "GPU 设备编号") int index,
            @Schema(description = "GPU 设备名称") String name,
            @Schema(description = "GPU 使用率百分比，不可用时为空") Double utilizationPercent,
            @Schema(description = "GPU 显存总量，单位 MiB") Long memoryTotalMiB,
            @Schema(description = "GPU 已用显存，单位 MiB") Long memoryUsedMiB) {}

    public ServerSnapshotVo asStale(String currentState) {
        return new ServerSnapshotVo(
                sampledAt, true, currentState, host, jvm, services, gpuStatus, gpus);
    }

    public static ServerSnapshotVo unavailable() {
        return new ServerSnapshotVo(
                null, true, "UNAVAILABLE", null, null, List.of(), "UNAVAILABLE", List.of());
    }
}
