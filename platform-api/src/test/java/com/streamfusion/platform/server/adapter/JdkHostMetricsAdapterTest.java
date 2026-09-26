package com.streamfusion.platform.server.adapter;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

import com.sun.management.OperatingSystemMXBean;
import java.util.concurrent.atomic.AtomicLong;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class JdkHostMetricsAdapterTest {
    @ParameterizedTest
    @ValueSource(strings = {"Windows 11", "Linux"})
    void discardsFirstReadAndUsesOneSecondWindowOnBothPlatforms(String name) {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getName()).thenReturn(name);
        when(os.getArch()).thenReturn("amd64");
        when(os.getAvailableProcessors()).thenReturn(8);
        when(os.getCpuLoad()).thenReturn(1.0, .125);
        when(os.getTotalMemorySize()).thenReturn(100L);
        when(os.getFreeMemorySize()).thenReturn(40L);
        var nano = new AtomicLong(100);
        var adapter = new JdkHostMetricsAdapter(os, nano::get);
        var start = adapter.beginCpuSample();
        nano.addAndGet(1_000_000_000L);
        var sample = adapter.finishCpuSample(start);
        assertThat(sample.cpuPercent()).isEqualTo(12.5);
        assertThat(sample.os()).isEqualTo(name);
        assertThat(sample.memoryUsedBytes()).isEqualTo(60L);
        verify(os, times(2)).getCpuLoad();
    }

    @ParameterizedTest
    @ValueSource(doubles = {-1, 1.01, Double.NaN, Double.POSITIVE_INFINITY})
    void invalidNativeReadingsRemainNullInsteadOfZeroOrClampedHundred(double value) {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getAvailableProcessors()).thenReturn(8);
        when(os.getCpuLoad()).thenReturn(0.0, value);
        when(os.getTotalMemorySize()).thenReturn(100L);
        when(os.getFreeMemorySize()).thenReturn(101L);
        var nano = new AtomicLong();
        var adapter = new JdkHostMetricsAdapter(os, nano::get);
        var start = adapter.beginCpuSample();
        nano.set(1_000_000_000L);
        var sample = adapter.finishCpuSample(start);
        assertThat(sample.cpuPercent()).isNull();
        assertThat(sample.memoryUsedBytes()).isNull();
    }

    @ParameterizedTest
    @ValueSource(doubles = {0, 1})
    void validIdleAndFullyBusyReadingsArePreserved(double value) {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getAvailableProcessors()).thenReturn(1);
        when(os.getCpuLoad()).thenReturn(-1.0, value);
        var nano = new AtomicLong();
        var adapter = new JdkHostMetricsAdapter(os, nano::get);
        var start = adapter.beginCpuSample();
        nano.set(1_000_000_000L);
        assertThat(adapter.finishCpuSample(start).cpuPercent()).isEqualTo(value * 100);
    }

    @Test
    void tooShortWindowChangedProcessorCountAndUnavailablePlatformReturnNull() {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getAvailableProcessors()).thenReturn(4);
        when(os.getCpuLoad()).thenReturn(.5);
        var nano = new AtomicLong();
        var adapter = new JdkHostMetricsAdapter(os, nano::get);
        var start = adapter.beginCpuSample();
        nano.set(999_999_999L);
        assertThat(adapter.finishCpuSample(start).cpuPercent()).isNull();
        nano.set(1_000_000_000L);
        when(os.getAvailableProcessors()).thenReturn(2);
        assertThat(adapter.finishCpuSample(start).cpuPercent()).isNull();
        verify(os, times(1)).getCpuLoad();
        var unsupported =
                new JdkHostMetricsAdapter(
                        mock(java.lang.management.OperatingSystemMXBean.class), nano::get);
        assertThat(unsupported.finishCpuSample(unsupported.beginCpuSample()).memoryTotalBytes())
                .isNull();
    }

    @Test
    void nativeExceptionsCannotFabricateCpuOrMemoryMetrics() {
        var os = mock(OperatingSystemMXBean.class);
        when(os.getCpuLoad()).thenThrow(new UnsupportedOperationException());
        when(os.getTotalMemorySize()).thenThrow(new UnsupportedOperationException());
        var adapter = new JdkHostMetricsAdapter(os, () -> 1_000_000_000L);
        assertThat(adapter.beginCpuSample()).isNull();
        var sample = adapter.finishCpuSample(null);
        assertThat(sample.cpuPercent()).isNull();
        assertThat(sample.memoryTotalBytes()).isNull();
    }
}
