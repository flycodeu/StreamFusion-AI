package com.streamfusion.platform.media.runtime.service;

import com.streamfusion.platform.media.runtime.adapter.MediaHostAdapter;
import com.streamfusion.platform.media.runtime.adapter.WindowsProcessMetadata;
import com.streamfusion.platform.media.runtime.config.MediaRuntimeProperties;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Diagnostic;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;
import java.util.stream.Stream;

/** Discovers known binary names through PATH, running processes and bounded configured roots. */
final class RuntimeDiscovery {
    private final MediaRuntimeProperties properties;
    private final MediaHostAdapter adapter;

    RuntimeDiscovery(MediaRuntimeProperties properties, MediaHostAdapter adapter) {
        this.properties = properties;
        this.adapter = adapter;
    }

    Result collect(long deadline) {
        var paths = adapter.paths(properties);
        Set<Path> executables = new LinkedHashSet<>();
        Set<Path> configs = new LinkedHashSet<>();
        List<Diagnostic> diagnostics = new ArrayList<>();
        paths.executables().stream().map(Path::of).forEach(path -> addBinary(path, executables));
        paths.zlmConfigs().stream()
                .map(Path::of)
                .filter(Files::isRegularFile)
                .forEach(configs::add);
        List<Running> running = processes(executables, configs, deadline);

        // PATH lookup checks only direct binary names; it never recursively searches PATH entries.
        String environmentPath =
                System.getenv().entrySet().stream()
                        .filter(entry -> entry.getKey().equalsIgnoreCase("PATH"))
                        .map(Map.Entry::getValue)
                        .findFirst()
                        .orElse("");
        for (String directory : environmentPath.split(Pattern.quote(adapter.pathSeparator()))) {
            if (expired(deadline)) break;
            try {
                Path root = Path.of(directory);
                if (!root.isAbsolute()) continue;
                addBinary(
                        root.resolve(adapter.os().equals("WINDOWS") ? "ffmpeg.exe" : "ffmpeg"),
                        executables);
                addBinary(
                        root.resolve(
                                adapter.os().equals("WINDOWS") ? "MediaServer.exe" : "MediaServer"),
                        executables);
            } catch (RuntimeException ignored) {
                addDiagnostic(diagnostics, "PATH_UNREADABLE", "部分PATH目录无法读取");
            }
        }
        Set<Path> roots =
                paths.searchRoots().stream()
                        .map(Path::of)
                        .collect(java.util.stream.Collectors.toCollection(LinkedHashSet::new));
        String extraRoots = System.getenv("SF_MEDIA_RUNTIME_ROOTS");
        if (extraRoots != null) {
            String[] entries = extraRoots.split(Pattern.quote(adapter.pathSeparator()));
            if (entries.length > 16) addDiagnostic(diagnostics, "ROOT_LIMIT", "仅检测前16个环境变量搜索目录");
            for (int i = 0; i < Math.min(16, entries.length); i++) {
                try {
                    Path root = Path.of(entries[i]);
                    if (root.isAbsolute()) roots.add(root.normalize());
                    else addDiagnostic(diagnostics, "ROOT_INVALID", "搜索目录必须使用绝对路径");
                } catch (RuntimeException ignored) {
                    addDiagnostic(diagnostics, "ROOT_INVALID", "部分搜索目录无效");
                }
            }
        }
        roots.addAll(adapter.defaultRoots(System.getenv()));
        int remaining = properties.maxEntries();
        for (Path root : roots) {
            if (expired(deadline) || remaining <= 0) break;
            if (!Files.isDirectory(root)) continue;
            // No FOLLOW_LINKS: symbolic links cannot expand a bounded root into another tree.
            try (Stream<Path> stream = Files.walk(root, properties.searchDepth())) {
                var iterator = stream.iterator();
                while (iterator.hasNext() && remaining-- > 0 && !expired(deadline)) {
                    Path file = iterator.next();
                    addBinary(file, executables);
                    if (file.getFileName() != null
                            && file.getFileName().toString().equalsIgnoreCase("config.ini")
                            && Files.isRegularFile(file))
                        configs.add(file.toAbsolutePath().normalize());
                }
            } catch (IOException | RuntimeException ignored) {
                addDiagnostic(diagnostics, "ROOT_UNREADABLE", "部分搜索目录不可读取");
            }
        }
        if (remaining <= 0) addDiagnostic(diagnostics, "SEARCH_LIMIT", "已达到搜索数量上限，可指定更精确的搜索目录");
        if (expired(deadline)) addDiagnostic(diagnostics, "DETECTION_TIMEOUT", "检测超时，已保留完成的检测结果");

        List<Discovered> discovered = new ArrayList<>();
        for (Path executable : executables) {
            String kind = adapter.kind(executable);
            List<Path> matches = new ArrayList<>();
            if ("ZLM".equals(kind)) {
                running.stream()
                        .filter(value -> value.executable().equals(executable))
                        .map(Running::config)
                        .filter(java.util.Objects::nonNull)
                        .forEach(matches::add);
                configs.stream()
                        .filter(config -> adjacent(executable, config))
                        .forEach(matches::add);
                Path sibling = executable.resolveSibling("config.ini");
                if (Files.isRegularFile(sibling)) matches.add(sibling);
                Path siblingConf = executable.getParent().resolve("conf/config.ini");
                if (Files.isRegularFile(siblingConf)) matches.add(siblingConf);
                // Explicit configuration is useful for Linux installations under /etc.
                if (matches.isEmpty())
                    matches.addAll(
                            paths.zlmConfigs().stream()
                                    .map(Path::of)
                                    .filter(Files::isRegularFile)
                                    .toList());
            }
            if (matches.isEmpty()) matches.add(null);
            for (Path config : matches.stream().distinct().toList()) {
                List<Long> pids =
                        running.stream()
                                .filter(value -> value.executable().equals(executable))
                                .map(Running::pid)
                                .distinct()
                                .sorted()
                                .toList();
                boolean matched =
                        running.stream()
                                .anyMatch(
                                        value ->
                                                value.executable().equals(executable)
                                                        && value.config() != null
                                                        && value.config().equals(config));
                discovered.add(new Discovered(kind, executable, config, pids, matched));
            }
        }
        discovered.sort(
                Comparator.comparing((Discovered value) -> value.pids().isEmpty())
                        .thenComparing(value -> value.executable().toString())
                        .thenComparing(
                                value -> value.config() == null ? "" : value.config().toString()));
        if (discovered.size() > properties.maxCandidates())
            addDiagnostic(diagnostics, "CANDIDATE_LIMIT", "候选版本过多，请缩小搜索目录");
        return new Result(
                discovered.stream().limit(properties.maxCandidates()).toList(), diagnostics);
    }

    private void addBinary(Path file, Set<Path> output) {
        if (file.getFileName() != null && adapter.kind(file) != null && Files.isRegularFile(file)) {
            output.add(file.toAbsolutePath().normalize());
        }
    }

    private List<Running> processes(Set<Path> executables, Set<Path> configs, long deadline) {
        List<Running> result = new ArrayList<>();
        try (Stream<ProcessHandle> processes = ProcessHandle.allProcesses()) {
            var iterator = processes.limit(properties.maxEntries()).iterator();
            while (iterator.hasNext() && !expired(deadline)) {
                ProcessHandle process = iterator.next();
                try {
                    ProcessHandle.Info info = process.info();
                    if (info.command().isEmpty()) continue;
                    Path executable = Path.of(info.command().get()).toAbsolutePath().normalize();
                    if (adapter.kind(executable) == null || !Files.isRegularFile(executable))
                        continue;
                    executables.add(executable);
                    java.util.Optional<String[]> arguments = info.arguments();
                    if (arguments.isEmpty()
                            && adapter.os().equals("WINDOWS")
                            && "ZLM".equals(adapter.kind(executable))
                            && !expired(deadline)) {
                        long remaining =
                                Math.max(
                                        1,
                                        Math.min(
                                                properties.probeTimeout().toMillis(),
                                                java.util.concurrent.TimeUnit.NANOSECONDS.toMillis(
                                                        deadline - System.nanoTime())));
                        arguments =
                                new WindowsProcessMetadata()
                                        .arguments(
                                                process.pid(),
                                                executable,
                                                Duration.ofMillis(remaining));
                    }
                    Path config =
                            arguments
                                    .map(args -> processConfig(executable, process.pid(), args))
                                    .orElse(null);
                    if (config != null) configs.add(config);
                    result.add(new Running(executable, config, process.pid()));
                } catch (RuntimeException ignored) {
                    // Other users' processes may deny access to command or arguments.
                }
            }
        } catch (RuntimeException ignored) {
        }
        return result;
    }

    private Path processConfig(Path executable, long pid, String[] args) {
        boolean supplied = false;
        for (int i = 0; i < args.length; i++) {
            String value = null;
            if ((args[i].equals("-c") || args[i].equals("--config")) && i + 1 < args.length)
                value = args[i + 1];
            if (args[i].startsWith("--config=")) value = args[i].substring(9);
            if (value == null) continue;
            supplied = true;
            try {
                Path candidate = Path.of(value);
                if (!candidate.isAbsolute()) {
                    Path cwd = executable.getParent();
                    if (adapter.os().equals("LINUX")) {
                        try {
                            cwd =
                                    Files.readSymbolicLink(
                                            Path.of("/proc", Long.toString(pid), "cwd"));
                        } catch (IOException ignored) {
                        }
                    }
                    candidate = cwd.resolve(candidate);
                }
                candidate = candidate.toAbsolutePath().normalize();
                if (Files.isRegularFile(candidate)) return candidate;
            } catch (RuntimeException ignored) {
            }
        }
        if (supplied) return null;
        // ZLM's default when no config argument was supplied is exeDir()/config.ini.
        Path defaultConfig = executable.resolveSibling("config.ini");
        return Files.isRegularFile(defaultConfig) ? defaultConfig : null;
    }

    private static boolean adjacent(Path executable, Path config) {
        Path parent = executable.getParent();
        return parent.equals(config.getParent())
                || parent.resolve("conf").equals(config.getParent());
    }

    static boolean expired(long deadline) {
        return Thread.currentThread().isInterrupted() || System.nanoTime() >= deadline;
    }

    static void addDiagnostic(List<Diagnostic> diagnostics, String code, String message) {
        if (diagnostics.stream().noneMatch(value -> value.code().equals(code)))
            diagnostics.add(new Diagnostic(code, message));
    }

    record Discovered(
            String kind,
            Path executable,
            Path config,
            List<Long> pids,
            boolean configurationMatched) {}

    record Result(List<Discovered> candidates, List<Diagnostic> diagnostics) {}

    private record Running(Path executable, Path config, long pid) {}
}
