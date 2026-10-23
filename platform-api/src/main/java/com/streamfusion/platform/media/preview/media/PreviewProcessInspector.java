package com.streamfusion.platform.media.preview.media;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.runtime.adapter.WindowsProcessMetadata;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import java.util.Locale;
import java.util.Optional;
import java.util.function.Supplier;
import java.util.stream.Stream;
import org.springframework.stereotype.Component;

/**
 * Read-only confirmation for local ZLM running as the platform user. Only executable paths visible
 * to that user are observable; this is not a host-wide process inventory.
 */
@Component
public class PreviewProcessInspector {
    private static final int MAX_PROCESSES = 2000;
    private static final int MAX_ARGUMENT_CHARS = 65536;
    private final Supplier<Stream<ProcessHandle>> processes;
    private final WindowsProcessMetadata windowsMetadata;
    private final boolean windows;

    public PreviewProcessInspector() {
        this(
                ProcessHandle::allProcesses,
                new WindowsProcessMetadata(),
                System.getProperty("os.name", "").toLowerCase(Locale.ROOT).startsWith("windows"));
    }

    PreviewProcessInspector(
            Supplier<Stream<ProcessHandle>> processes,
            WindowsProcessMetadata windowsMetadata,
            boolean windows) {
        this.processes = processes;
        this.windowsMetadata = windowsMetadata;
        this.windows = windows;
    }

    public boolean exited(
            ActiveZlm runtime,
            String app,
            String inputId,
            String outputId,
            PreviewDeadline deadline) {
        if (runtime == null
                || !loopback(runtime.apiBase())
                || app == null
                || !app.matches("sf_preview_[a-f0-9]{12}")
                || inputId == null
                || !inputId.matches("[a-f0-9]{32}")
                || !(inputId + "h264").equals(outputId)) return false;
        try (var values = processes.get()) {
            var iterator = values.iterator();
            int count = 0;
            while (iterator.hasNext()) {
                deadline.check();
                if (++count > MAX_PROCESSES) return false;
                ProcessHandle process = iterator.next();
                if (!process.isAlive()) continue;
                var info = process.info();
                // System/other-user processes may hide their executable entirely.
                if (info.command().isEmpty()) continue;
                Path executable = Path.of(info.command().get());
                if (!ffmpeg(executable)) continue;
                Optional<String[]> arguments = info.arguments();
                if (arguments.isEmpty() && windows) {
                    Duration remaining = deadline.remaining();
                    arguments =
                            windowsMetadata.arguments(
                                    process.pid(),
                                    executable,
                                    remaining.compareTo(Duration.ofSeconds(2)) < 0
                                            ? remaining
                                            : Duration.ofSeconds(2));
                }
                deadline.check();
                // A process may have exited during its metadata lookup.
                if (!process.isAlive()) continue;
                if (arguments.isEmpty()
                        || arguments.get().length == 0
                        || arguments.get().length > 4096) return false;
                int chars = 0;
                for (String argument : arguments.get()) {
                    if (argument == null || (chars += argument.length()) > MAX_ARGUMENT_CHARS)
                        return false;
                    if (matches(argument, "rtsp", app, inputId)
                            || matches(argument, "rtmp", app, outputId)
                            || matches(argument, "rtsp", app, outputId)) return false;
                }
            }
            deadline.check();
            return true;
        } catch (RuntimeException unavailable) {
            return false;
        }
    }

    private boolean ffmpeg(Path executable) {
        if (executable.getFileName() == null) return false;
        String name = executable.getFileName().toString();
        return windows ? "ffmpeg.exe".equalsIgnoreCase(name) : "ffmpeg".equals(name);
    }

    private static boolean matches(String argument, String scheme, String app, String id) {
        try {
            URI uri = URI.create(argument);
            return scheme.equalsIgnoreCase(uri.getScheme())
                    && loopback(uri)
                    && ("/" + app + "/" + id).equals(uri.getPath());
        } catch (IllegalArgumentException ignored) {
            return false;
        }
    }

    private static boolean loopback(URI uri) {
        if (uri == null) return false;
        String host = uri.getHost();
        return "127.0.0.1".equals(host)
                || "::1".equals(host)
                || "[::1]".equals(host)
                || "localhost".equalsIgnoreCase(host);
    }
}
