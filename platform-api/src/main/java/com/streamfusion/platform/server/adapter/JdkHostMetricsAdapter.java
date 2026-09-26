package com.streamfusion.platform.server.adapter;

import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.lang.management.ManagementFactory;
import java.lang.management.OperatingSystemMXBean;
import java.util.function.LongSupplier;
import org.springframework.stereotype.Component;

/** The JDK provides Windows/Linux native adapters without mixing host /proc and container data. */
@Component
public class JdkHostMetricsAdapter implements HostMetricsAdapter {
    private final OperatingSystemMXBean base;
    private final com.sun.management.OperatingSystemMXBean extended;
    private final LongSupplier nanoTime;

    public JdkHostMetricsAdapter() {
        this(ManagementFactory.getOperatingSystemMXBean(), System::nanoTime);
    }

    JdkHostMetricsAdapter(OperatingSystemMXBean base, LongSupplier nanoTime) {
        this.base = base;
        this.extended = base instanceof com.sun.management.OperatingSystemMXBean os ? os : null;
        this.nanoTime = nanoTime;
    }

    @Override
    public CpuSample beginCpuSample() {
        // Discard both the first native sample and the interval since the previous cached refresh.
        try {
            if (extended != null) extended.getCpuLoad();
        } catch (RuntimeException ignored) {
            return null;
        }
        return new CpuSample(nanoTime.getAsLong(), base.getAvailableProcessors());
    }

    @Override
    public ServerSnapshotVo.Host finishCpuSample(CpuSample sample) {
        int processors = base.getAvailableProcessors();
        boolean validWindow =
                sample != null
                        && nanoTime.getAsLong() - sample.startedAtNanos() >= CPU_WINDOW.toNanos()
                        && sample.processors() > 0
                        && sample.processors() == processors;
        Double cpu = null;
        if (extended != null && validWindow) {
            try {
                cpu = percent(extended.getCpuLoad());
            } catch (RuntimeException ignored) {
            }
        }
        Long total = extended == null ? null : readMemory(extended::getTotalMemorySize);
        Long free = extended == null ? null : readMemory(extended::getFreeMemorySize);
        Long used = total == null || free == null || free > total ? null : total - free;
        return new ServerSnapshotVo.Host(
                base.getName(), base.getArch(), processors, cpu, total, used);
    }

    private static Long readMemory(LongSupplier read) {
        try {
            long value = read.getAsLong();
            return value < 0 ? null : value;
        } catch (RuntimeException ignored) {
            return null;
        }
    }

    private static Double percent(double value) {
        return Double.isFinite(value) && value >= 0 && value <= 1 ? value * 100 : null;
    }
}
