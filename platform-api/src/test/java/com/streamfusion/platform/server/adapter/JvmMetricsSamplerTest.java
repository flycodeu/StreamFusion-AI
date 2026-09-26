package com.streamfusion.platform.server.adapter;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

import com.sun.management.OperatingSystemMXBean;
import java.lang.management.MemoryMXBean;
import java.lang.management.MemoryUsage;
import java.lang.management.RuntimeMXBean;
import java.util.concurrent.atomic.AtomicLong;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

class JvmMetricsSamplerTest {
    @Test
    void computesFromActualElapsedNanosAndAvailableProcessors() {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getAvailableProcessors()).thenReturn(4);
        when(os.getProcessCpuTime()).thenReturn(100L, 2_000_000_100L);
        var nano = new AtomicLong();
        var sampler = sampler(os, nano);
        var start = sampler.beginCpuSample();
        nano.set(2_000_000_000L);
        assertThat(sampler.finishCpuSample(start).cpuPercent()).isEqualTo(25.0);
        verify(os, never()).getProcessCpuLoad();
    }

    @ParameterizedTest
    @CsvSource({
        "-1,100,1000000000,4",
        "100,99,1000000000,4",
        "0,100,999999999,4",
        "0,100,1000000000,0",
        "0,5000000000,1000000000,4"
    })
    void rejectsUnavailableRegressedShortOrOutOfRangeSamples(
            long before, long after, long elapsed, int cpus) {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getAvailableProcessors()).thenReturn(cpus);
        when(os.getProcessCpuTime()).thenReturn(before, after);
        var nano = new AtomicLong();
        var sampler = sampler(os, nano);
        var start = sampler.beginCpuSample();
        nano.set(elapsed);
        assertThat(sampler.finishCpuSample(start).cpuPercent()).isNull();
    }

    @Test
    void rejectsProcessorChangesAndUnsupportedCpuTime() {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getAvailableProcessors()).thenReturn(4, 2);
        when(os.getProcessCpuTime()).thenReturn(0L, 100L);
        var nano = new AtomicLong();
        var sampler = sampler(os, nano);
        var start = sampler.beginCpuSample();
        nano.set(1_000_000_000L);
        assertThat(sampler.finishCpuSample(start).cpuPercent()).isNull();
        when(os.getProcessCpuTime()).thenThrow(new UnsupportedOperationException());
        start = sampler.beginCpuSample();
        nano.addAndGet(1_000_000_000L);
        assertThat(sampler.finishCpuSample(start).cpuPercent()).isNull();
    }

    private JvmMetricsSampler sampler(OperatingSystemMXBean os, AtomicLong nano) {
        var memory = mock(MemoryMXBean.class);
        when(memory.getHeapMemoryUsage()).thenReturn(new MemoryUsage(0, 20, 50, 100));
        when(memory.getNonHeapMemoryUsage()).thenReturn(new MemoryUsage(0, 10, 20, -1));
        return new JvmMetricsSampler(os, memory, mock(RuntimeMXBean.class), nano::get);
    }
}
