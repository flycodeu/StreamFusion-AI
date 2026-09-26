package com.streamfusion.platform.server.adapter;

import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.lang.management.ManagementFactory;
import java.lang.management.MemoryMXBean;
import java.lang.management.OperatingSystemMXBean;
import java.lang.management.RuntimeMXBean;
import java.time.Instant;
import java.util.function.LongSupplier;
import org.springframework.stereotype.Component;

/** JVM CPU is normalized to the JDK-visible logical processor count, not fractional CPU quotas. */
@Component
public class JvmMetricsSampler {
    private final OperatingSystemMXBean base;
    private final com.sun.management.OperatingSystemMXBean extended;
    private final MemoryMXBean memory;
    private final RuntimeMXBean runtime;
    private final LongSupplier nanoTime;

    public JvmMetricsSampler() {
        this(
                ManagementFactory.getOperatingSystemMXBean(),
                ManagementFactory.getMemoryMXBean(),
                ManagementFactory.getRuntimeMXBean(),
                System::nanoTime);
    }

    JvmMetricsSampler(
            OperatingSystemMXBean base,
            MemoryMXBean memory,
            RuntimeMXBean runtime,
            LongSupplier nanoTime) {
        this.base = base;
        this.extended = base instanceof com.sun.management.OperatingSystemMXBean os ? os : null;
        this.memory = memory;
        this.runtime = runtime;
        this.nanoTime = nanoTime;
    }

    public CpuSample beginCpuSample() {
        return new CpuSample(nanoTime.getAsLong(), processCpuTime(), base.getAvailableProcessors());
    }

    public ServerSnapshotVo.Jvm finishCpuSample(CpuSample sample) {
        long processTime = processCpuTime();
        long elapsed = nanoTime.getAsLong() - sample.startedAtNanos();
        Double cpu = null;
        if (sample.processCpuTime() >= 0
                && processTime >= sample.processCpuTime()
                && elapsed >= HostMetricsAdapter.CPU_WINDOW.toNanos()
                && sample.processors() > 0
                && sample.processors() == base.getAvailableProcessors()) {
            double load =
                    (double) (processTime - sample.processCpuTime())
                            / elapsed
                            / sample.processors();
            if (Double.isFinite(load) && load >= 0 && load <= 1) cpu = load * 100;
        }
        return new ServerSnapshotVo.Jvm(
                System.getProperty("java.version"),
                Long.toString(ProcessHandle.current().pid()),
                cpu,
                memory.getHeapMemoryUsage().getUsed(),
                memory.getHeapMemoryUsage().getMax(),
                memory.getNonHeapMemoryUsage().getUsed(),
                runtime.getUptime(),
                Instant.ofEpochMilli(runtime.getStartTime()));
    }

    private long processCpuTime() {
        try {
            return extended == null ? -1 : extended.getProcessCpuTime();
        } catch (RuntimeException ignored) {
            return -1;
        }
    }

    public record CpuSample(long startedAtNanos, long processCpuTime, int processors) {}
}
