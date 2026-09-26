package com.streamfusion.platform.server.adapter;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import org.junit.jupiter.api.Test;

class NvidiaGpuAdapterTest {
    @Test
    void usesFixedExecutablePerPlatformWithoutAShell() {
        assertThat(NvidiaGpuAdapter.command("Windows 11").getFirst()).isEqualTo("nvidia-smi.exe");
        assertThat(NvidiaGpuAdapter.command("Linux").getFirst()).isEqualTo("nvidia-smi");
        assertThat(NvidiaGpuAdapter.command("Linux")).hasSize(3);
        assertThat(NvidiaGpuAdapter.command("Mac OS X")).isEmpty();
    }

    @Test
    void parsesOnlyBoundedMetricsAndNeverReturnsCommandDiagnostics() {
        var result = NvidiaGpuAdapter.parse("0, NVIDIA Test, 25, 8192, 2048\n");
        assertThat(result.status()).isEqualTo("AVAILABLE");
        assertThat(result.devices().getFirst().memoryUsedMiB()).isEqualTo(2048);
        assertThat(NvidiaGpuAdapter.parse("secret error text").devices()).isEmpty();
        assertThat(NvidiaGpuAdapter.parse("0, Test, 25, 1, 1\n".repeat(33)).status())
                .isEqualTo("UNAVAILABLE");
        var missing = NvidiaGpuAdapter.parse("0, Test, N/A, -1, N/A").devices().getFirst();
        assertThat(missing.utilizationPercent()).isNull();
        assertThat(missing.memoryTotalMiB()).isNull();
        assertThat(missing.memoryUsedMiB()).isNull();
        assertThat(
                        NvidiaGpuAdapter.parse("0, Test, NaN, 1, 1")
                                .devices()
                                .getFirst()
                                .utilizationPercent())
                .isNull();
        assertThat(
                        NvidiaGpuAdapter.parse("0, Test, 101, 1, 1")
                                .devices()
                                .getFirst()
                                .utilizationPercent())
                .isNull();
    }

    @Test
    void readsSuccessfulProcessAndRejectsOversizedOutputAndExitFailure() throws Exception {
        Process success = process("0, Test, 20, 100, 10\n");
        assertThat(adapter(success).collect().status()).isEqualTo("AVAILABLE");
        Process oversized = process("x".repeat(16385));
        when(oversized.isAlive()).thenReturn(true);
        assertThat(adapter(oversized).collect().status()).isEqualTo("UNAVAILABLE");
        verify(oversized).destroyForcibly();
        Process failed = process("sensitive diagnostic");
        when(failed.exitValue()).thenReturn(1);
        assertThat(adapter(failed).collect().devices()).isEmpty();
    }

    @Test
    void killsTimedOutProcessesAndHandlesMissingExecutable() throws Exception {
        Process hung = process("");
        when(hung.isAlive()).thenReturn(true);
        assertThat(adapter(hung).collect().status()).isEqualTo("UNAVAILABLE");
        verify(hung).destroyForcibly();
        var missing =
                new NvidiaGpuAdapter(
                        "Linux",
                        command -> {
                            throw new IOException("private path");
                        },
                        Duration.ofMillis(20));
        assertThat(missing.collect().devices()).isEmpty();
        assertThat(missing.collect().status()).isEqualTo("UNAVAILABLE");
    }

    @Test
    void drainsBytesArrivingBetweenEmptyCheckAndObservedExit() throws Exception {
        Process process = process("unused");
        var stream =
                spy(
                        new ByteArrayInputStream(
                                "0, Tail GPU, 20, 100, 10\n".getBytes(StandardCharsets.UTF_8)));
        when(stream.available()).thenReturn(0).thenCallRealMethod();
        when(process.getInputStream()).thenReturn(stream);
        assertThat(adapter(process).collect().devices().getFirst().name()).isEqualTo("Tail GPU");
    }

    @Test
    void neverStartsAnotherProcessUntilTimedOutProcessPhysicallyExits() throws Exception {
        Process hung = process("");
        when(hung.isAlive()).thenReturn(true);
        var starter = mock(NvidiaGpuAdapter.ProcessStarter.class);
        when(starter.start(any())).thenReturn(hung);
        var adapter = new NvidiaGpuAdapter("Windows 11", starter, Duration.ofMillis(20));
        assertThat(adapter.collect().status()).isEqualTo("UNAVAILABLE");
        assertThat(adapter.collect().status()).isEqualTo("UNAVAILABLE");
        verify(starter, times(1)).start(any());
        when(hung.isAlive()).thenReturn(false);
        Process recovered = process("0, Recovered GPU, 20, 100, 10\n");
        when(starter.start(any())).thenReturn(recovered);
        assertThat(adapter.collect().status()).isEqualTo("AVAILABLE");
        verify(starter, times(2)).start(any());
    }

    private NvidiaGpuAdapter adapter(Process process) {
        return new NvidiaGpuAdapter("Linux", command -> process, Duration.ofMillis(30));
    }

    private Process process(String output) {
        Process process = mock(Process.class);
        when(process.getInputStream())
                .thenReturn(new ByteArrayInputStream(output.getBytes(StandardCharsets.UTF_8)));
        when(process.getOutputStream()).thenReturn(new ByteArrayOutputStream());
        when(process.getErrorStream()).thenReturn(new ByteArrayInputStream(new byte[0]));
        return process;
    }
}
