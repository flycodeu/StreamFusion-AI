package com.streamfusion.platform.server.adapter;

import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import org.springframework.stereotype.Component;

/**
 * One fixed NVIDIA query; no shell, user commands, unbounded output or diagnostics in responses.
 */
@Component
public class NvidiaGpuAdapter {
    private static final int MAX_OUTPUT_BYTES = 16384;
    private final String osName;
    private final ProcessStarter starter;
    private final Duration timeout;
    private Process pendingTermination;

    public NvidiaGpuAdapter() {
        this(
                System.getProperty("os.name", ""),
                command -> new ProcessBuilder(command).redirectErrorStream(true).start(),
                Duration.ofSeconds(2));
    }

    NvidiaGpuAdapter(String osName, ProcessStarter starter, Duration timeout) {
        this.osName = osName;
        this.starter = starter;
        this.timeout = timeout;
    }

    public synchronized GpuResult collect() {
        if (pendingTermination != null) {
            if (pendingTermination.isAlive()) return unavailable();
            pendingTermination = null;
        }
        List<String> command = command(osName);
        if (command.isEmpty() || Thread.currentThread().isInterrupted()) return unavailable();
        Process process = null;
        try {
            process = starter.start(command);
            long started = System.nanoTime();
            var output = new ByteArrayOutputStream();
            var stream = process.getInputStream();
            byte[] buffer = new byte[1024];
            boolean exited = false;
            while (true) {
                if (Thread.currentThread().isInterrupted()) return unavailable();
                if (System.nanoTime() - started >= timeout.toNanos()) return unavailable();
                int available = stream.available();
                if (available > 0) {
                    int count = stream.read(buffer, 0, Math.min(available, buffer.length));
                    if (count > 0) output.write(buffer, 0, count);
                    if (output.size() > MAX_OUTPUT_BYTES) return unavailable();
                    continue;
                }
                if (exited) {
                    if (process.exitValue() != 0) return unavailable();
                    return parse(output.toString(StandardCharsets.UTF_8));
                }
                if (!process.isAlive()) {
                    // Recheck the pipe after observing exit: final bytes may have arrived between
                    // available() and isAlive().
                    exited = true;
                    continue;
                }
                Thread.sleep(10);
            }
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            return unavailable();
        } catch (IOException | RuntimeException ignored) {
            return unavailable();
        } finally {
            if (process != null) {
                if (process.isAlive()) {
                    process.destroyForcibly();
                    // destroyForcibly is only a request. Do not spawn again until physical exit.
                    if (process.isAlive()) pendingTermination = process;
                }
                try {
                    process.getInputStream().close();
                } catch (IOException ignored) {
                }
                try {
                    process.getOutputStream().close();
                } catch (IOException ignored) {
                }
                try {
                    process.getErrorStream().close();
                } catch (IOException ignored) {
                }
            }
        }
    }

    static List<String> command(String osName) {
        String normalized = osName.toLowerCase(Locale.ROOT);
        String executable;
        if (normalized.startsWith("windows")) executable = "nvidia-smi.exe";
        else if (normalized.equals("linux")) executable = "nvidia-smi";
        else return List.of();
        return List.of(
                executable,
                "--query-gpu=index,name,utilization.gpu,memory.total,memory.used",
                "--format=csv,noheader,nounits");
    }

    static GpuResult parse(String csv) {
        if (csv.length() > MAX_OUTPUT_BYTES) return unavailable();
        List<ServerSnapshotVo.Gpu> result = new ArrayList<>();
        try {
            for (String line : csv.lines().toList()) {
                if (line.isBlank()) continue;
                if (result.size() >= 32) return unavailable();
                String[] fields = line.split(",", -1);
                if (fields.length != 5
                        || fields[1].strip().isEmpty()
                        || fields[1].strip().length() > 128) return unavailable();
                int index = Integer.parseInt(fields[0].strip());
                if (index < 0) return unavailable();
                result.add(
                        new ServerSnapshotVo.Gpu(
                                index,
                                fields[1].strip(),
                                metric(fields[2]),
                                memory(fields[3]),
                                memory(fields[4])));
            }
            return new GpuResult(
                    result.isEmpty() ? "UNAVAILABLE" : "AVAILABLE", List.copyOf(result));
        } catch (RuntimeException ignored) {
            return unavailable();
        }
    }

    private static Double metric(String value) {
        try {
            double number = Double.parseDouble(value.strip());
            return Double.isFinite(number) && number >= 0 && number <= 100 ? number : null;
        } catch (NumberFormatException ignored) {
            return null;
        }
    }

    private static Long memory(String value) {
        try {
            long number = Long.parseLong(value.strip());
            return number < 0 ? null : number;
        } catch (NumberFormatException ignored) {
            return null;
        }
    }

    private static GpuResult unavailable() {
        return new GpuResult("UNAVAILABLE", List.of());
    }

    @FunctionalInterface
    interface ProcessStarter {
        Process start(List<String> command) throws IOException;
    }

    public record GpuResult(String status, List<ServerSnapshotVo.Gpu> devices) {}
}
