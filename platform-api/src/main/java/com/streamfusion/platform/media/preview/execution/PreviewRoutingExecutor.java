package com.streamfusion.platform.media.preview.execution;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewModels;
import com.streamfusion.platform.media.preview.node.PreviewNodeManager;
import com.streamfusion.platform.media.preview.node.PreviewNodeRepository;
import com.streamfusion.platform.media.preview.node.RemotePreviewExecutor;
import com.streamfusion.platform.media.preview.node.ZlmDirectExecutor;
import java.net.URI;
import java.util.List;
import java.util.Set;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.context.annotation.Primary;
import org.springframework.stereotype.Component;

/** Pins every admitted viewer to its source assignment until confirmed cleanup. */
@Primary
@Component
public final class PreviewRoutingExecutor implements PreviewExecutor {
    private final LocalPreviewExecutor local;
    private final RemotePreviewExecutor remote;
    private final ZlmDirectExecutor direct;
    private final PreviewNodeManager manager;
    private final PreviewNodeRepository repository;
    private final com.streamfusion.platform.media.preview.node.PreviewScheduler scheduler;
    private final java.util.Map<String, Integer> retries =
            new java.util.concurrent.ConcurrentHashMap<>();
    private final java.util.Map<String, List<String>> requestedCodecs =
            new java.util.concurrent.ConcurrentHashMap<>();

    @Autowired
    public PreviewRoutingExecutor(
            LocalPreviewExecutor local,
            RemotePreviewExecutor remote,
            ZlmDirectExecutor direct,
            PreviewNodeManager manager,
            PreviewNodeRepository repository,
            com.streamfusion.platform.media.preview.node.PreviewScheduler scheduler) {
        this.local = local;
        this.remote = remote;
        this.direct = direct;
        this.manager = manager;
        this.repository = repository;
        this.scheduler = scheduler;
    }

    public PreviewRoutingExecutor(
            LocalPreviewExecutor local,
            RemotePreviewExecutor remote,
            PreviewNodeManager manager,
            PreviewNodeRepository repository,
            com.streamfusion.platform.media.preview.node.PreviewScheduler scheduler) {
        this(local, remote, null, manager, repository, scheduler);
    }

    @Override
    public void requireRecovered() {
        /* Recovery is checked for each candidate node. */
    }

    @Override
    public void prepare(String id) {
        throw new PreviewFailure("SOURCE_REQUIRED");
    }

    @Override
    public void prepare(String id, SourceKey key, List<String> codecs, PreviewDeadline deadline) {
        var node = scheduler.reserve(id, key, codecs, deadline);
        requestedCodecs.put(id, List.copyOf(codecs));
        if (node.local()) {
            local.prepare(id);
            local.allowTranscoding(id, repository.transcodeAllowed(id));
        } else if (node.zlm() != null) direct.prepared(id);
        else remote.prepared(id);
    }

    private PreviewExecutor route(String id) {
        var viewer = repository.viewer(id);
        if (viewer == null) throw new PreviewFailure("LEASE_EXPIRED");
        var node = repository.node(viewer.nodeId());
        if (node == null) throw new PreviewFailure("NODE_IDENTITY_CHANGED");
        return node.local() ? local : node.zlm() != null ? direct : remote;
    }

    @Override
    public void attachSource(
            String id,
            SourceKey key,
            Supplier<URI> resolver,
            Runnable checkpoint,
            PreviewDeadline deadline) {
        var viewer = repository.viewer(id);
        if (viewer == null) throw new PreviewFailure("LEASE_EXPIRED");
        var node = repository.node(viewer.nodeId());
        if (node == null || node.zlm() == null) {
            route(id).attachSource(id, key, resolver, checkpoint, deadline);
            return;
        }
        try {
            direct.attachSource(id, key, resolver, checkpoint, deadline);
        } catch (PreviewFailure failure) {
            if (!ZlmDirectExecutor.CODEC_FALLBACK.equals(failure.reason())) throw failure;
            // The direct pull has an exact persisted tuple. Only confirmed cleanup permits
            // assigning the same source to another node.
            direct.close(id, deadline);
            scheduler.closed(id);
            var codecs = requestedCodecs.get(id);
            if (codecs == null) throw new PreviewFailure("SOURCE_REQUIRED");
            var next = scheduler.reserve(id, key, codecs, deadline, Set.of(node.nodeId()));
            if (next.local()) {
                local.prepare(id);
                local.allowTranscoding(id, repository.transcodeAllowed(id));
            } else if (next.zlm() != null) {
                direct.prepared(id);
            } else remote.prepared(id);
            route(id).attachSource(id, key, resolver, checkpoint, deadline);
        }
    }

    @Override
    public Playback start(
            String id, List<String> codecs, Runnable checkpoint, PreviewDeadline deadline) {
        var playback = route(id).start(id, codecs, checkpoint, deadline);
        scheduler.ready(id, playback);
        return playback;
    }

    @Override
    public void confirmReady(String id, PreviewDeadline deadline) {
        route(id).confirmReady(id, deadline);
    }

    @Override
    public void renew(String id, PreviewDeadline deadline) {
        route(id).renew(id, deadline);
    }

    @Override
    public void requireCurrent(String id) {
        route(id).requireCurrent(id);
    }

    @Override
    public List<String> missingSourceViewers(String id, PreviewDeadline deadline) {
        return route(id).missingSourceViewers(id, deadline);
    }

    @Override
    public PreviewModels.Answer answer(String id, String sdp, PreviewDeadline deadline) {
        return route(id).answer(id, sdp, deadline);
    }

    @Override
    public PreviewModels.Stats stats(String id, PreviewDeadline deadline) {
        return route(id).stats(id, deadline);
    }

    @Override
    public void close(String id, PreviewDeadline deadline) {
        var viewer = repository.viewer(id);
        if (viewer == null) {
            retries.remove(id);
            requestedCodecs.remove(id);
            return;
        }
        synchronized (manager) {
            repository.state(id, "CLOSING");
        }
        var node = repository.node(viewer.nodeId());
        if (node == null) throw new PreviewFailure("NODE_IDENTITY_CHANGED");
        if (node.local()) {
            if (!scheduler.current(id)) local.requireRecovered();
            local.close(id, deadline);
        } else if (node.zlm() != null) direct.close(id, deadline);
        else remote.close(id, deadline, true);
        scheduler.closed(id);
        manager.invalidate(viewer.nodeId());
        retries.remove(id);
        requestedCodecs.remove(id);
    }

    @Override
    public int occupiedTranscodes() {
        int total = local.occupiedTranscodes();
        for (var node : repository.nodes())
            if (!node.local()
                    && node.zlm() == null
                    && (!repository.viewers(node.nodeId()).isEmpty()
                            || node.mode().equals("REMOTE")))
                total +=
                        manager.observation(
                                        node,
                                        PreviewDeadline.after(java.time.Duration.ofSeconds(2)))
                                .occupiedTranscodes();
        return total;
    }

    @Override
    public int sessionLimit(int localLimit) {
        return Math.max(
                0,
                repository.nodes().stream()
                        .filter(
                                n ->
                                        n.mode().equals("REMOTE")
                                                || !repository.viewers(n.nodeId()).isEmpty())
                        .mapToInt(
                                n ->
                                        n.local()
                                                ? Math.min(localLimit, n.budget().maxViewers())
                                                : n.budget().maxViewers())
                        .sum());
    }

    @Override
    public int retainedViewers(java.util.Set<String> activeLeaseIds) {
        return (int)
                repository.viewers().stream()
                        .filter(viewer -> !activeLeaseIds.contains(viewer.id()))
                        .count();
    }

    @Override
    public int transcodeLimit(int localLimit) {
        return repository.nodes().stream()
                .filter(n -> n.mode().equals("REMOTE") || !repository.viewers(n.nodeId()).isEmpty())
                .mapToInt(
                        n ->
                                n.local()
                                        ? Math.min(localLimit, n.budget().maxTranscodes())
                                        : n.budget().maxTranscodes())
                .sum();
    }

    @Override
    public void reconcile(BooleanSupplier stopping) {
        local.reconcile(stopping);
        int attempted = 0;
        for (var viewer : repository.viewers()) {
            if (stopping.getAsBoolean() || attempted >= 4) break;
            if (scheduler.current(viewer.id())
                    || !repository.scheduled(viewer.id())
                    || retries.getOrDefault(viewer.id(), 0) >= 6) continue;
            attempted++;
            retries.merge(viewer.id(), 1, Integer::sum);
            try {
                close(viewer.id(), PreviewDeadline.after(java.time.Duration.ofSeconds(3)));
            } catch (PreviewFailure ignored) {
            }
        }
        if (!stopping.getAsBoolean()) remote.reconcile(stopping);
    }

    public void retryRecovery() {
        retries.clear();
        remote.retryRecovery();
    }
}
