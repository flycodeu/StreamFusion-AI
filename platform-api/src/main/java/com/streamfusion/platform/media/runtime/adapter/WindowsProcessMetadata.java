package com.streamfusion.platform.media.runtime.adapter;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;

/** Windows JDK exposes process paths but often omits arguments; CIM supplies read-only metadata. */
public final class WindowsProcessMetadata {
    private static final int MAX_BYTES = 65536;

    public Optional<String[]> arguments(long pid, Path executable, Duration timeout) {
        if (pid < 1 || timeout.isZero() || timeout.isNegative()) return Optional.empty();
        Path shell =
                Path.of(
                        System.getenv().getOrDefault("SystemRoot", "C:\\Windows"),
                        "System32",
                        "WindowsPowerShell",
                        "v1.0",
                        "powershell.exe");
        // Only an already observed numeric process ID is interpolated; no paths or user commands.
        String script =
                "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); "
                        + "Get-CimInstance Win32_Process -Filter 'ProcessId="
                        + pid
                        + "' -OperationTimeoutSec 1 "
                        + "| Select-Object ProcessId,ExecutablePath,CommandLine | ConvertTo-Json -Compress";
        Process process = null;
        try {
            process =
                    new ProcessBuilder(
                                    shell.toString(),
                                    "-NoProfile",
                                    "-NonInteractive",
                                    "-Command",
                                    script)
                            .redirectErrorStream(true)
                            .start();
            long deadline = System.nanoTime() + timeout.toNanos();
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            var input = process.getInputStream();
            boolean exited = false;
            byte[] buffer = new byte[1024];
            while (System.nanoTime() < deadline && !Thread.currentThread().isInterrupted()) {
                int available = input.available();
                if (available > 0) {
                    int count = input.read(buffer, 0, Math.min(buffer.length, available));
                    if (count > 0) bytes.write(buffer, 0, count);
                    if (bytes.size() > MAX_BYTES) return Optional.empty();
                    continue;
                }
                if (exited)
                    return process.exitValue() == 0
                            ? parseResult(bytes.toString(StandardCharsets.UTF_8), pid, executable)
                            : Optional.empty();
                if (!process.isAlive()) {
                    exited = true;
                    continue;
                }
                Thread.sleep(10);
            }
            return Optional.empty();
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            return Optional.empty();
        } catch (IOException | RuntimeException ignored) {
            return Optional.empty();
        } finally {
            if (process != null) {
                if (process.isAlive()) process.destroyForcibly();
                try {
                    process.getInputStream().close();
                    process.getErrorStream().close();
                    process.getOutputStream().close();
                } catch (IOException ignored) {
                }
            }
        }
    }

    static Optional<String[]> parseResult(String json, long pid, Path executable) {
        try {
            if (json.length() > MAX_BYTES) return Optional.empty();
            JsonNode value = new ObjectMapper().readTree(json);
            if (value == null
                    || value.path("ProcessId").asLong(-1) != pid
                    || !value.path("ExecutablePath").isTextual()
                    || !value.path("CommandLine").isTextual()) return Optional.empty();
            Path actual =
                    Path.of(value.path("ExecutablePath").asText()).toAbsolutePath().normalize();
            if (!actual.equals(executable.toAbsolutePath().normalize())) return Optional.empty();
            List<String> tokens = splitCommandLine(value.path("CommandLine").asText());
            if (tokens.isEmpty()) return Optional.empty();
            return Optional.of(tokens.subList(1, tokens.size()).toArray(String[]::new));
        } catch (IOException | RuntimeException ignored) {
            return Optional.empty();
        }
    }

    /** Windows quote/backslash rules; shell expansion is never performed. */
    static List<String> splitCommandLine(String command) {
        List<String> result = new ArrayList<>();
        int index = 0;
        while (index < command.length()) {
            while (index < command.length() && Character.isWhitespace(command.charAt(index)))
                index++;
            if (index == command.length()) break;
            StringBuilder token = new StringBuilder();
            boolean quoted = false;
            while (index < command.length()) {
                char value = command.charAt(index);
                if (!quoted && Character.isWhitespace(value)) break;
                int slashes = 0;
                while (index < command.length() && command.charAt(index) == '\\') {
                    slashes++;
                    index++;
                }
                if (index < command.length() && command.charAt(index) == '"') {
                    token.append("\\".repeat(slashes / 2));
                    if (slashes % 2 != 0) token.append('"');
                    else if (quoted
                            && index + 1 < command.length()
                            && command.charAt(index + 1) == '"') {
                        token.append('"');
                        index++;
                    } else quoted = !quoted;
                    index++;
                } else {
                    token.append("\\".repeat(slashes));
                    if (index < command.length()) {
                        if (!quoted && Character.isWhitespace(command.charAt(index))) break;
                        token.append(command.charAt(index++));
                    }
                }
            }
            if (quoted) return List.of();
            result.add(token.toString());
        }
        return List.copyOf(result);
    }
}
