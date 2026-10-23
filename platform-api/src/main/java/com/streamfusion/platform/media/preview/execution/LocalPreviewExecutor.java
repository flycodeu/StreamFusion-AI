package com.streamfusion.platform.media.preview.execution;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewModels;
import com.streamfusion.platform.media.preview.PreviewPlaybackPlanner;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.preview.media.PreviewPullRegistry;
import com.streamfusion.platform.media.preview.media.PreviewTranscodeRegistry;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import java.net.URI;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;
import org.springframework.stereotype.Component;

/** Owns local media resources; user identity and business authorization remain in the platform. */
@Component
public final class LocalPreviewExecutor implements PreviewExecutor {
    private final PreviewAdmissionProtection protection = new PreviewAdmissionProtection();
    private volatile com.streamfusion.platform.media.preview.control.PreviewBudget capacity =
            com.streamfusion.platform.media.preview.control.PreviewBudget.defaults(64, 64);
    private int observationCursor;
    private long nextObservation;

    public void budget(com.streamfusion.platform.media.preview.control.PreviewBudget budget) {
        capacity = budget;
    }

    @Override
    public PreviewAdmissionProtection.Observation protection() {
        return protection.observation();
    }

    @Override
    public void requireAdmission() {
        var reason = protection.reason();
        if (reason != null) throw new PreviewFailure(reason);
    }

    private final PreviewMediaGateway media;
    private final PreviewRuntimeProvider runtimes;
    private final PreviewPullRegistry pulls;
    private final PreviewTranscodeRegistry transcodes;
    private final PreviewRecoveryCoordinator recovery;
    private final PreviewSharedTranscodes sharedTranscodes;
    private final String app =
            "sf_preview_" + UUID.randomUUID().toString().replace("-", "").substring(0, 12);
    private final Map<String, LocalViewer> viewers = new ConcurrentHashMap<>();
    // Membership and closing use guard; no network calls hold it.
    private final Object guard = new Object();
    private final Map<UpstreamKey, SharedUpstream> upstreams = new HashMap<>();

    public LocalPreviewExecutor(
            PreviewMediaGateway media,
            PreviewRuntimeProvider runtimes,
            PreviewPullRegistry pulls,
            PreviewTranscodeRegistry transcodes,
            PreviewRecoveryCoordinator recovery) {
        this.media = media;
        this.runtimes = runtimes;
        this.pulls = pulls;
        this.transcodes = transcodes;
        this.recovery = recovery;
        this.sharedTranscodes = new PreviewSharedTranscodes(media, pulls);
    }

    @Override
    public void requireRecovered() {
        recovery.requireRecovered();
    }

    public void requireAvailable() {
        requireRecovered();
        requireAdmission();
        if (runtimes.activeZlm().isEmpty()) throw new PreviewFailure("ZLM_NOT_READY");
    }

    /** Pins source/output reservations to the actual local media configuration. */
    public String configurationVersion() {
        var runtime = runtimes.activeZlm().orElseThrow(() -> new PreviewFailure("ZLM_NOT_READY"));
        try {
            if (java.nio.file.Files.size(runtime.configPath()) > 262144)
                throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
            var digest = java.security.MessageDigest.getInstance("SHA-256");
            digest.update(
                    (runtime.apiBase()
                                    + "\n"
                                    + runtime.executablePath()
                                    + "\n"
                                    + runtimes.activeFfmpeg().map(Object::toString).orElse(""))
                            .getBytes(java.nio.charset.StandardCharsets.UTF_8));
            digest.update(java.nio.file.Files.readAllBytes(runtime.configPath()));
            return java.util.HexFormat.of().formatHex(digest.digest());
        } catch (java.io.IOException | java.security.GeneralSecurityException failure) {
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        }
    }

    @Override
    public void prepare(String viewerId) {
        var runtime = runtimes.activeZlm().orElseThrow(() -> new PreviewFailure("ZLM_NOT_READY"));
        viewers.putIfAbsent(viewerId, new LocalViewer(viewerId, runtime));
    }

    private final Set<String> directOnly = ConcurrentHashMap.newKeySet();

    @Override
    public void allowTranscoding(String id, boolean allowed) {
        if (!allowed) directOnly.add(id);
        else directOnly.remove(id);
    }

    @Override
    public void attachSource(
            String viewerId,
            SourceKey sourceKey,
            Supplier<URI> resolver,
            Runnable checkpoint,
            PreviewDeadline deadline) {
        var viewer = require(viewerId);
        var runtime = viewer.runtime;
        var key = new UpstreamKey(sourceKey, runtime);
        SharedUpstream shared;
        boolean creator;
        synchronized (guard) {
            // The lease operation stays registered until this attachment has settled.
            deadline.check();
            shared = upstreams.get(key);
            creator = shared == null;
            if (creator) {
                String id = UUID.randomUUID().toString().replace("-", "");
                shared =
                        new SharedUpstream(
                                key,
                                new Stream(runtime, app, id, "__defaultVhost__/" + app + "/" + id));
                upstreams.put(key, shared);
            }
            if (shared.closing) throw new PreviewFailure("PREVIEW_PENDING");
            shared.members.add(viewerId);
            viewer.upstream = shared;
        }
        if (creator) {
            try {
                var source = resolver.get();
                checkpoint.run();
                // Record the exact tuple before mutation, even for unknown network outcomes.
                pulls.register(shared.stream, false);
                shared.openAttempted = true;
                shared.stream = media.open(runtime, app, shared.stream.id(), source, deadline);
                pulls.started(shared.stream);
                checkpoint.run();
                shared.opened.complete(shared.stream);
            } catch (RuntimeException failure) {
                shared.opened.completeExceptionally(failure);
                throw failure;
            }
        }
        try {
            // Each joining viewer keeps its own finite budget and authorization lifecycle.
            shared.opened.get(deadline.remaining().toNanos(), TimeUnit.NANOSECONDS);
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (java.util.concurrent.TimeoutException ex) {
            throw new PreviewFailure("PREVIEW_PENDING");
        } catch (ExecutionException ex) {
            // A creator's authorization failure is not the joining viewer's identity result.
            if (ex.getCause() instanceof PreviewFailure failure) throw failure;
            throw new PreviewFailure("PREVIEW_UNAVAILABLE");
        }
    }

    @Override
    public Playback start(
            String viewerId,
            List<String> videoCodecs,
            Runnable checkpoint,
            PreviewDeadline deadline) {
        var viewer = require(viewerId);
        var upstream = viewer.upstream.stream;
        var sourceStats = awaitTracks(upstream, upstream.id(), checkpoint, deadline);
        var decision = PreviewPlaybackPlanner.decide(sourceStats.videoCodec(), videoCodecs);
        if (decision == PreviewPlaybackPlanner.Decision.UNSUPPORTED)
            throw new PreviewFailure("CODEC_UNSUPPORTED");
        boolean normalizeAudio =
                !directOnly.contains(viewerId)
                        && videoCodecs.contains("H264")
                        && media.requiresAudioTranscode(upstream, deadline);
        if (decision == PreviewPlaybackPlanner.Decision.TRANSCODE_H264 || normalizeAudio) {
            if (directOnly.contains(viewerId)) throw new PreviewFailure("TRANSCODE_CAPACITY");
            upstream = sharedTranscodes.acquire(viewerId, upstream, deadline);
            checkpoint.run();
            var encoded = awaitTracks(upstream, upstream.id(), checkpoint, deadline);
            if (!"H264".equals(PreviewPlaybackPlanner.normalize(encoded.videoCodec())))
                throw new PreviewFailure("TRANSCODE_CODEC_MISMATCH");
            viewer.transcoded = true;
        }
        checkpoint.run();
        String id = viewerId.replace("-", "");
        synchronized (viewer) {
            checkpoint.run();
            // Retain the exact tuple before mutation, including timeout with unknown outcome.
            var pending = new Stream(viewer.runtime, app, id, "__defaultVhost__/" + app + "/" + id);
            pulls.register(pending, true);
            viewer.stream = pending;
        }
        var stream = media.openViewer(upstream, app, id, deadline);
        pulls.started(stream);
        synchronized (viewer) {
            viewer.stream = stream;
        }
        checkpoint.run();
        var stats = awaitTracks(stream, id, checkpoint, deadline);
        if (viewer.transcoded
                && !"H264".equals(PreviewPlaybackPlanner.normalize(stats.videoCodec())))
            throw new PreviewFailure("TRANSCODE_CODEC_MISMATCH");
        if (!videoCodecs.contains(PreviewPlaybackPlanner.normalize(stats.videoCodec())))
            throw new PreviewFailure("CODEC_UNSUPPORTED");
        return new Playback(
                PreviewPlaybackPlanner.normalize(stats.videoCodec()),
                viewer.transcoded,
                PreviewPlaybackPlanner.normalize(sourceStats.videoCodec()));
    }

    private PreviewModels.Stats awaitTracks(
            Stream stream, String output, Runnable checkpoint, PreviewDeadline deadline) {
        for (int attempt = 0; attempt < 4; attempt++) {
            checkpoint.run();
            var stats = media.stats(stream, output, deadline);
            checkpoint.run();
            if ("READY".equals(stats.state()) && stats.videoCodec() != null) return stats;
            if (attempt < 3) {
                try {
                    TimeUnit.NANOSECONDS.sleep(
                            Math.min(
                                    TimeUnit.MILLISECONDS.toNanos(200),
                                    deadline.remaining().toNanos()));
                } catch (InterruptedException ex) {
                    Thread.currentThread().interrupt();
                    throw new PreviewFailure("CANCELLED");
                }
            }
        }
        throw new PreviewFailure("MEDIA_NOT_READY");
    }

    @Override
    public void confirmReady(String viewerId, PreviewDeadline deadline) {
        require(viewerId).upstream.established = true;
    }

    @Override
    public void requireCurrent(String viewerId) {
        var viewer = viewers.get(viewerId);
        if (viewer != null
                && viewer.upstream != null
                && runtimes.activeZlm().filter(viewer.runtime::equals).isEmpty())
            throw new PreviewFailure("ZLM_NOT_READY");
    }

    @Override
    public List<String> missingSourceViewers(String viewerId, PreviewDeadline deadline) {
        var viewer = viewers.get(viewerId);
        var shared = viewer == null ? null : viewer.upstream;
        if (shared == null || !shared.established || media.exists(shared.stream, deadline))
            return List.of();
        synchronized (guard) {
            shared.closing = true;
            return List.copyOf(shared.members);
        }
    }

    @Override
    public PreviewModels.Answer answer(String viewerId, String sdp, PreviewDeadline deadline) {
        var stream = require(viewerId).stream;
        return media.answer(stream, stream.id(), sdp, deadline);
    }

    @Override
    public PreviewModels.Stats stats(String viewerId, PreviewDeadline deadline) {
        var stream = require(viewerId).stream;
        return media.stats(stream, stream.id(), deadline);
    }

    @Override
    public void close(String viewerId, PreviewDeadline deadline) {
        directOnly.remove(viewerId);
        var viewer = viewers.get(viewerId);
        if (viewer == null) return;
        // The lease coordinator waits for active operations and serializes cleanup attempts.
        if (viewer.stream != null && !viewer.viewerClosed) {
            media.close(viewer.stream, null, deadline);
            pulls.exited(viewer.stream);
            viewer.viewerClosed = true;
        }
        sharedTranscodes.release(viewerId, deadline);
        detachUpstream(viewer, deadline);
        viewers.remove(viewerId, viewer);
    }

    private void detachUpstream(LocalViewer viewer, PreviewDeadline deadline) {
        SharedUpstream shared;
        synchronized (guard) {
            shared = viewer.upstream;
            if (shared == null) return;
            if (shared.members.size() > 1) {
                shared.members.remove(viewer.id);
                viewer.upstream = null;
                return;
            }
            // Retain the last member and its session slot until upstream cleanup is confirmed.
            shared.closing = true;
        }
        if (shared.openAttempted) {
            media.close(shared.stream, null, deadline);
            pulls.exited(shared.stream);
        }
        synchronized (guard) {
            shared.members.remove(viewer.id);
            upstreams.remove(shared.key, shared);
            viewer.upstream = null;
        }
    }

    @Override
    public int occupiedTranscodes() {
        return transcodes.occupied();
    }

    @Override
    public void reconcile(BooleanSupplier stopping) {
        recovery.reconcile(stopping);
        observeLoad(stopping);
    }

    private synchronized void observeLoad(BooleanSupplier stopping) {
        long now = System.nanoTime();
        if (stopping.getAsBoolean() || now < nextObservation) return;
        nextObservation = now + java.util.concurrent.TimeUnit.SECONDS.toNanos(5);
        protection.sampleHost();
        var active = List.copyOf(viewers.values());
        var keys = new HashSet<String>();
        for (var v : active) {
            keys.add(v.id);
            if (v.upstream != null) keys.add(v.upstream.stream.id());
        }
        protection.retain(keys);
        var sampled = new HashSet<String>();
        var deadline = PreviewDeadline.after(java.time.Duration.ofSeconds(2));
        for (int i = 0; i < Math.min(4, active.size()) && !stopping.getAsBoolean(); i++) {
            var v = active.get(Math.floorMod(observationCursor++, active.size()));
            try {
                if (v.upstream != null
                        && v.upstream.established
                        && sampled.add(v.upstream.stream.id())) {
                    var stream = v.upstream.stream;
                    protection.sampleBandwidth(
                            stream.id(),
                            media.stats(stream, stream.id(), deadline).mediaKbps(),
                            capacity.sourceUpperKbps());
                }
                if (v.stream != null)
                    protection.sampleBandwidth(
                            v.id,
                            media.stats(v.stream, v.stream.id(), deadline).mediaKbps(),
                            capacity.outputUpperKbps());
                deadline.check();
            } catch (RuntimeException unavailable) {
                break;
            }
        }
    }

    private LocalViewer require(String viewerId) {
        var viewer = viewers.get(viewerId);
        if (viewer == null) throw new PreviewFailure("MEDIA_NOT_READY");
        return viewer;
    }

    private record UpstreamKey(SourceKey source, MediaRuntimeService.ActiveZlm runtime) {}

    private static final class SharedUpstream {
        final UpstreamKey key;
        final Set<String> members = new HashSet<>();
        final CompletableFuture<Stream> opened = new CompletableFuture<>();
        volatile Stream stream;
        volatile boolean openAttempted;
        volatile boolean established;
        boolean closing;

        SharedUpstream(UpstreamKey key, Stream stream) {
            this.key = key;
            this.stream = stream;
        }
    }

    private static final class LocalViewer {
        final String id;
        final MediaRuntimeService.ActiveZlm runtime;
        volatile SharedUpstream upstream;
        volatile Stream stream;
        boolean transcoded;
        boolean viewerClosed;

        LocalViewer(String id, MediaRuntimeService.ActiveZlm runtime) {
            this.id = id;
            this.runtime = runtime;
        }
    }
}
