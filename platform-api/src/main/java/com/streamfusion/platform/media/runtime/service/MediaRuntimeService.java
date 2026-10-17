package com.streamfusion.platform.media.runtime.service;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.media.runtime.adapter.MediaHostAdapter;
import com.streamfusion.platform.media.runtime.config.MediaRuntimeProperties;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Candidate;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Diagnostic;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Selection;
import java.net.URI;
import java.net.http.HttpClient;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.locks.ReentrantLock;
import java.util.stream.Collectors;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.boot.web.context.WebServerApplicationContext;
import org.springframework.context.event.EventListener;
import org.springframework.stereotype.Service;

/** Coordinates discovery, read-only probes and the platform's selected runtime versions. */
@Service
public class MediaRuntimeService
        implements com.streamfusion.platform.media.preview.execution.PreviewRuntimeProvider {
    private static final Logger LOG = LoggerFactory.getLogger(MediaRuntimeService.class);
    private final MediaRuntimeProperties properties;
    private final MediaHostAdapter adapter;
    private final RuntimeProbe probe;
    private final RuntimeSelectionStore selections;
    private final ReentrantLock detectionLock = new ReentrantLock();
    private volatile Inventory inventory;

    @Autowired
    public MediaRuntimeService(
            MediaRuntimeProperties properties, PlatformProperties platform, ObjectMapper mapper) {
        this(
                properties,
                platform,
                mapper,
                System.getProperty("os.name", ""),
                HttpClient.newBuilder()
                        .connectTimeout(properties.probeTimeout())
                        .followRedirects(HttpClient.Redirect.NEVER)
                        .build());
    }

    MediaRuntimeService(
            MediaRuntimeProperties properties,
            PlatformProperties platform,
            ObjectMapper mapper,
            String osName,
            HttpClient http) {
        this.properties = properties;
        this.adapter = MediaHostAdapter.forOs(osName);
        this.probe = new RuntimeProbe(properties, mapper, http);
        this.selections = new RuntimeSelectionStore(platform.workDir(), mapper);
    }

    @EventListener(ApplicationReadyEvent.class)
    public void onReady(ApplicationReadyEvent event) {
        if (!properties.autoSelectOnStartup()
                || !(event.getApplicationContext() instanceof WebServerApplicationContext)) return;
        try (var ignored =
                MDC.putCloseable("traceId", UUID.randomUUID().toString().replace("-", ""))) {
            try {
                initializeSelection();
            } catch (RuntimeException ex) {
                LOG.warn(
                        "Media runtime startup matching unavailable errorType={}",
                        ex.getClass().getSimpleName());
            }
        }
    }

    synchronized void initializeSelection() {
        var detected = detect();
        if (adapter.os().equals("UNSUPPORTED")) return;
        var matched =
                matchSelection(detected.selected(), detected.candidates(), inventory.zlmConfigs());
        if (!matched.equals(detected.selected())) {
            selections.update(adapter.os(), matched, detected.version());
            LOG.info(
                    "Media runtime startup selection matched zlmId={} ffmpegId={}",
                    matched.zlmId(),
                    matched.ffmpegId());
        }
    }

    static Selection matchSelection(
            Selection saved,
            List<Candidate> candidates,
            Map<String, RuntimeProbe.ZlmConfig> configs) {
        String zlmId = matchCandidate(saved.zlmId(), "ZLM", candidates, null);
        var config = zlmId == null ? null : configs.get(zlmId);
        String ffmpegId =
                matchCandidate(
                        saved.ffmpegId(),
                        "FFMPEG",
                        candidates,
                        config == null ? null : config.ffmpegBin());
        return new Selection(zlmId, ffmpegId);
    }

    private static String matchCandidate(
            String savedId, String kind, List<Candidate> candidates, String preferredPath) {
        var sameKind =
                candidates.stream().filter(candidate -> candidate.kind().equals(kind)).toList();
        if (savedId != null
                && sameKind.stream().anyMatch(candidate -> candidate.id().equals(savedId)))
            return savedId;
        var ready =
                sameKind.stream().filter(candidate -> candidate.status().equals("READY")).toList();
        if (preferredPath != null) {
            var preferred =
                    ready.stream()
                            .filter(
                                    candidate ->
                                            matchesFfmpeg(
                                                    preferredPath, candidate.executablePath()))
                            .toList();
            if (preferred.size() == 1) return preferred.getFirst().id();
        }
        return ready.size() == 1 ? ready.getFirst().id() : savedId;
    }

    public MediaRuntimeSnapshot current() {
        Inventory value = inventory;
        if (value == null
                || System.nanoTime() - value.detectedAt() >= properties.cacheTtl().toNanos())
            return detect();
        return snapshot(value);
    }

    public MediaRuntimeSnapshot detect() {
        if (!detectionLock.tryLock()) {
            Inventory previous = inventory;
            if (previous == null) throw BusinessException.error(ErrorCode.RATE_LIMITED);
            return snapshot(previous);
        }
        try {
            Inventory previous = inventory;
            // Coalesce simultaneous browser refreshes without spawning repeated external commands.
            if (previous != null
                    && System.nanoTime() - previous.detectedAt() < TimeUnit.SECONDS.toNanos(1))
                return snapshot(previous);
            Instant now = Instant.now();
            long deadline = System.nanoTime() + properties.collectionTimeout().toNanos();
            if (adapter.os().equals("UNSUPPORTED")) {
                inventory =
                        new Inventory(
                                List.of(),
                                Map.of(),
                                now,
                                System.nanoTime(),
                                List.of(new Diagnostic("UNSUPPORTED_OS", "当前操作系统尚未提供媒体适配器")));
            } else {
                RuntimeDiscovery.Result found =
                        new RuntimeDiscovery(properties, adapter).collect(deadline);
                List<RuntimeProbe.Probed> checked =
                        found.candidates().stream()
                                .map(candidate -> probe.inspect(candidate, now, deadline))
                                .toList();
                inventory =
                        new Inventory(
                                checked.stream().map(RuntimeProbe.Probed::candidate).toList(),
                                checked.stream()
                                        .filter(candidate -> candidate.zlm() != null)
                                        .collect(
                                                Collectors.toMap(
                                                        candidate -> candidate.candidate().id(),
                                                        RuntimeProbe.Probed::zlm)),
                                now,
                                System.nanoTime(),
                                List.copyOf(found.diagnostics()));
            }
            return snapshot(inventory);
        } finally {
            detectionLock.unlock();
        }
    }

    public synchronized MediaRuntimeSnapshot select(
            String zlmId, String ffmpegId, String expectedVersion) {
        MediaRuntimeSnapshot current = current();
        if (adapter.os().equals("UNSUPPORTED")) throw BusinessException.error(ErrorCode.CONFLICT);
        validateSelection(current, zlmId, "ZLM");
        validateSelection(current, ffmpegId, "FFMPEG");
        selections.update(adapter.os(), new Selection(zlmId, ffmpegId), expectedVersion);
        return snapshot(inventory);
    }

    private static void validateSelection(MediaRuntimeSnapshot current, String id, String kind) {
        if (id != null
                && current.candidates().stream()
                        .noneMatch(
                                candidate ->
                                        candidate.id().equals(id)
                                                && candidate.kind().equals(kind))) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
    }

    private MediaRuntimeSnapshot snapshot(Inventory value) {
        RuntimeSelectionStore.State saved = selections.load();
        Selection effective = saved.selection(adapter.os());
        List<Diagnostic> diagnostics = new ArrayList<>(value.diagnostics());
        if (missing(effective.zlmId(), value) || missing(effective.ffmpegId(), value)) {
            RuntimeDiscovery.addDiagnostic(
                    diagnostics, "SELECTED_VERSION_MISSING", "已选版本当前未被发现，请检查搜索目录或重新选择");
        }
        RuntimeProbe.ZlmConfig zlm =
                effective.zlmId() == null ? null : value.zlmConfigs().get(effective.zlmId());
        Candidate ffmpeg =
                value.candidates().stream()
                        .filter(candidate -> candidate.id().equals(effective.ffmpegId()))
                        .findFirst()
                        .orElse(null);
        if (zlm != null
                && ffmpeg != null
                && zlm.ffmpegBin() != null
                && !matchesFfmpeg(zlm.ffmpegBin(), ffmpeg.executablePath())) {
            RuntimeDiscovery.addDiagnostic(
                    diagnostics, "FFMPEG_CONFIG_MISMATCH", "尚未确认所选FFmpeg与ZLM当前配置一致，请在运行时管理端核对程序路径");
        }
        return new MediaRuntimeSnapshot(
                adapter.os(),
                Long.toString(saved.revision()),
                value.checkedAt(),
                effective,
                value.candidates(),
                List.copyOf(diagnostics));
    }

    private static boolean matchesFfmpeg(String configured, String selected) {
        try {
            Path path = Path.of(configured);
            // A relative executable is resolved by ZLM's own environment, so equality is
            // unconfirmed.
            return path.isAbsolute() && path.normalize().equals(Path.of(selected).normalize());
        } catch (RuntimeException ignored) {
            return false;
        }
    }

    private static boolean missing(String id, Inventory value) {
        return id != null
                && value.candidates().stream().noneMatch(candidate -> candidate.id().equals(id));
    }

    /**
     * Reads the last discovered selection without starting discovery in a preview request. Startup
     * matching and the runtime-management page own refreshes; the gateway checks actual readiness.
     * API secrets must not be returned by controllers or placed in logs.
     */
    public Optional<ActiveZlm> activeZlm() {
        Inventory value = inventory;
        if (value == null) return Optional.empty();
        MediaRuntimeSnapshot snapshot = snapshot(value);
        return snapshot.candidates().stream()
                .filter(candidate -> candidate.id().equals(snapshot.selected().zlmId()))
                .filter(
                        candidate ->
                                candidate.kind().equals("ZLM")
                                        && candidate.status().equals("READY")
                                        && candidate.configPath() != null
                                        && candidate.apiBase() != null)
                .flatMap(
                        candidate -> {
                            RuntimeProbe.ZlmConfig config = value.zlmConfigs().get(candidate.id());
                            if (config == null) return java.util.stream.Stream.empty();
                            return java.util.stream.Stream.of(
                                    new ActiveZlm(
                                            config.apiBase(),
                                            config.secret(),
                                            Path.of(candidate.executablePath()),
                                            Path.of(candidate.configPath()),
                                            candidate.softwareVersion()));
                        })
                .findFirst();
    }

    public Optional<ActiveFfmpeg> activeFfmpeg() {
        Inventory value = inventory;
        if (value == null) return Optional.empty();
        MediaRuntimeSnapshot snapshot = snapshot(value);
        return snapshot.candidates().stream()
                .filter(candidate -> candidate.id().equals(snapshot.selected().ffmpegId()))
                .filter(
                        candidate ->
                                candidate.kind().equals("FFMPEG")
                                        && candidate.status().equals("READY"))
                .map(
                        candidate ->
                                new ActiveFfmpeg(
                                        Path.of(candidate.executablePath()),
                                        candidate.softwareVersion()))
                .findFirst();
    }

    /** Recovery only uses already-discovered, trusted runtimes, never paths/URLs from a journal. */
    public Optional<ActiveZlm> recoveryZlm(String api, String configPath) {
        Inventory value = inventory;
        if (value == null) return Optional.empty();
        return snapshot(value).candidates().stream()
                .filter(
                        c ->
                                c.kind().equals("ZLM")
                                        && c.status().equals("READY")
                                        && c.configPath() != null)
                .filter(
                        c ->
                                Path.of(c.configPath())
                                        .toAbsolutePath()
                                        .normalize()
                                        .toString()
                                        .equals(configPath))
                .flatMap(
                        c -> {
                            var config = value.zlmConfigs().get(c.id());
                            if (config == null || !config.apiBase().toString().equals(api))
                                return java.util.stream.Stream.empty();
                            return java.util.stream.Stream.of(
                                    new ActiveZlm(
                                            config.apiBase(),
                                            config.secret(),
                                            Path.of(c.executablePath()),
                                            Path.of(c.configPath()),
                                            c.softwareVersion()));
                        })
                .findFirst();
    }

    public record ActiveZlm(
            URI apiBase, String secret, Path executablePath, Path configPath, String version) {
        @Override
        public String toString() {
            return "ActiveZlm[apiBase=" + apiBase + ", secret=<redacted>, version=" + version + "]";
        }
    }

    public record ActiveFfmpeg(Path executablePath, String version) {}

    private record Inventory(
            List<Candidate> candidates,
            Map<String, RuntimeProbe.ZlmConfig> zlmConfigs,
            Instant checkedAt,
            long detectedAt,
            List<Diagnostic> diagnostics) {}
}
