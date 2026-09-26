package com.streamfusion.platform.server.adapter;

import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.time.Duration;

/** Host scope is the JVM operating environment, including container limits reported by the JDK. */
public interface HostMetricsAdapter {
    Duration CPU_WINDOW = Duration.ofSeconds(1);

    CpuSample beginCpuSample();

    ServerSnapshotVo.Host finishCpuSample(CpuSample sample);

    record CpuSample(long startedAtNanos, int processors) {}
}
