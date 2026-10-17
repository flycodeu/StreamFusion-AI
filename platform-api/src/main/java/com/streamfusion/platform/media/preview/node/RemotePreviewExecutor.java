package com.streamfusion.platform.media.preview.node;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewModels;
import com.streamfusion.platform.media.preview.control.PreviewControl.*;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import java.net.URI;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;
import org.slf4j.MDC;
import org.springframework.stereotype.Component;

/** A pinned remote executor. Unknown requests retain their original node and durable ownership. */
@Component
public final class RemotePreviewExecutor implements PreviewExecutor {
    private final PreviewNodeRepository repository;
    private final PreviewNodeManager manager;
    private final ObjectMapper json;
    private final Map<String, PendingSource> sources = new ConcurrentHashMap<>();
    private final java.util.Set<String> current = ConcurrentHashMap.newKeySet();
    private final Map<String, Integer> recoveryAttempts = new ConcurrentHashMap<>();

    public RemotePreviewExecutor(
            PreviewNodeRepository repository, PreviewNodeManager manager, ObjectMapper json) {
        this.repository = repository;
        this.manager = manager;
        this.json = json;
    }

    @Override
    public void requireRecovered() {
        if (repository.viewers().stream().anyMatch(v -> !current.contains(v.id())))
            throw new PreviewFailure("NODE_RECONCILING");
    }

    @Override
    public void prepare(String id) {
        synchronized (manager) {
            var node = requireNode();
            if (!node.mode().equals("REMOTE")) throw new PreviewFailure("NODE_DRAINING");
            requireRecovered();
            if (repository.viewers().size() >= 64) throw new PreviewFailure("PREVIEW_CAPACITY");
            var snapshot = manager.inspect(node, PreviewDeadline.after(Duration.ofSeconds(4)));
            if (!snapshot.state().equals("READY")
                    || !snapshot.configVersion().equals(node.configVersion()))
                throw new PreviewFailure("NODE_RECONCILING");
            repository.prepare(
                    new PreviewNodeRepository.Viewer(
                            id,
                            node.connection().nodeId(),
                            snapshot.bootId(),
                            snapshot.configVersion(),
                            "RESERVED"));
            current.add(id);
        }
    }

    public void prepared(String id) {
        current.add(id);
    }

    public boolean current(String id) {
        return current.contains(id);
    }

    @Override
    public void attachSource(
            String id,
            SourceKey source,
            Supplier<URI> resolver,
            Runnable checkpoint,
            PreviewDeadline deadline) {
        checkpoint.run();
        synchronized (manager) {
            repository.source(id, source.profileId(), source.connectionKey());
        }
        URI uri = repository.canReuseSource(id) ? null : resolver.get();
        checkpoint.run();
        deadline.check();
        sources.put(id, new PendingSource(source, uri));
    }

    @Override
    public Playback start(
            String id, List<String> videoCodecs, Runnable checkpoint, PreviewDeadline deadline) {
        var source = sources.remove(id);
        if (source == null) throw new PreviewFailure("MEDIA_NOT_READY");
        checkpoint.run();
        var result = apply(id, Action.CREATE, source, videoCodecs, null, deadline);
        checkpoint.run();
        try {
            var playback = json.treeToValue(result.result(), Playback.class);
            if (playback == null || !videoCodecs.contains(playback.codec()))
                throw new PreviewFailure("NODE_INVALID_RESPONSE");
            repository.state(id, "READY");
            return playback;
        } catch (java.io.IOException ex) {
            throw new PreviewFailure("NODE_INVALID_RESPONSE");
        }
    }

    @Override
    public void confirmReady(String id, PreviewDeadline deadline) {
        renew(id, deadline);
    }

    @Override
    public void renew(String id, PreviewDeadline deadline) {
        apply(id, Action.RENEW, null, null, null, deadline);
    }

    @Override
    public void requireCurrent(String id) {
        var viewer = repository.viewer(id);
        var node = requireNode(id);
        if (viewer == null
                || !viewer.nodeId().equals(node.connection().nodeId())
                || !viewer.configVersion().equals(node.configVersion()))
            throw new PreviewFailure("NODE_IDENTITY_CHANGED");
    }

    @Override
    public List<String> missingSourceViewers(String id, PreviewDeadline deadline) {
        var viewer = repository.viewer(id);
        if (viewer == null || viewer.state().equals("RESERVED")) return List.of();
        try (var client = manager.client(requireNode(id).connection())) {
            var resource = client.get("resources/" + id, Resource.class, deadline);
            if (!resource.state().equals("READY") || !resource.bootId().equals(viewer.bootId()))
                return List.of(id);
            return List.of();
        }
    }

    @Override
    public PreviewModels.Answer answer(String id, String sdp, PreviewDeadline deadline) {
        var receipt = apply(id, Action.ANSWER, null, null, sdp, deadline);
        try {
            var answer = json.treeToValue(receipt.result(), PreviewModels.Answer.class);
            if (answer == null
                    || answer.sdp() == null
                    || !answer.sdp().startsWith("v=0")
                    || answer.sdp().length() > 65536)
                throw new PreviewFailure("NODE_INVALID_RESPONSE");
            return answer;
        } catch (java.io.IOException ex) {
            throw new PreviewFailure("NODE_INVALID_RESPONSE");
        }
    }

    @Override
    public PreviewModels.Stats stats(String id, PreviewDeadline deadline) {
        requireCurrent(id);
        try (var client = manager.client(requireNode(id).connection())) {
            return client.get("resources/" + id + "/stats", PreviewModels.Stats.class, deadline);
        }
    }

    @Override
    public void close(String id, PreviewDeadline deadline) {
        close(id, deadline, false);
    }

    public void close(String id, PreviewDeadline deadline, boolean retainLedger) {
        sources.remove(id);
        var viewer = repository.viewer(id);
        if (viewer == null) {
            current.remove(id);
            return;
        }
        repository.state(id, "CLOSING");
        var node = requireNode(id);
        var snapshot = manager.inspect(node, deadline.within(Duration.ofSeconds(4)));
        if (!snapshot.bootId().equals(viewer.bootId())) {
            // Old boot writes are forbidden. READY means the node confirmed all old media exited.
            if (!snapshot.state().equals("READY")
                    || snapshot.resources().stream().anyMatch(r -> r.resourceId().equals(id)))
                throw new PreviewFailure("CLEANUP_PENDING");
        } else apply(id, Action.STOP, null, null, null, deadline);
        if (!retainLedger) {
            repository.resolved(id);
            repository.closed(id);
        }
        current.remove(id);
        recoveryAttempts.remove(id);
    }

    @Override
    public int occupiedTranscodes() {
        return manager.inspect(requireNode(), PreviewDeadline.after(Duration.ofSeconds(3)))
                .occupiedTranscodes();
    }

    @Override
    public void reconcile(BooleanSupplier stopping) {
        int count = 0;
        for (var viewer : repository.viewers()) {
            if (stopping.getAsBoolean() || count >= 4) break;
            if (viewer.nodeId().equals("local")
                    || repository.node(viewer.nodeId()) == null
                    || repository.node(viewer.nodeId()).zlm() != null
                    || repository.scheduled(viewer.id())
                    || current.contains(viewer.id())
                    || recoveryAttempts.getOrDefault(viewer.id(), 0) >= 6) continue;
            count++;
            recoveryAttempts.merge(viewer.id(), 1, Integer::sum);
            try {
                close(viewer.id(), PreviewDeadline.after(Duration.ofSeconds(3)));
            } catch (PreviewFailure ignored) {
                /* Ownership remains in SQL for inspection and retry. */
            }
        }
    }

    public void retryRecovery() {
        recoveryAttempts.clear();
    }

    private Receipt apply(
            String id,
            Action action,
            PendingSource source,
            List<String> codecs,
            String sdp,
            PreviewDeadline deadline) {
        var viewer = repository.viewer(id);
        if (viewer == null) throw new PreviewFailure("LEASE_EXPIRED");
        var node = requireNode(id);
        String trace = MDC.get("traceId");
        if (trace == null || !trace.matches("[A-Za-z0-9_-]{1,64}"))
            trace = UUID.randomUUID().toString();
        var command =
                new Command(
                        1,
                        viewer.nodeId(),
                        viewer.bootId(),
                        UUID.randomUUID().toString(),
                        trace,
                        id,
                        1,
                        viewer.configVersion(),
                        System.currentTimeMillis()
                                + Math.min(18000, deadline.remaining().toMillis()),
                        action,
                        source == null ? null : source.key(),
                        source == null || source.uri() == null ? null : source.uri().toString(),
                        codecs,
                        sdp,
                        repository.scheduled(id) ? repository.transcodeAllowed(id) : null);
        try {
            repository.operation(
                    command,
                    HexFormat.of()
                            .formatHex(
                                    MessageDigest.getInstance("SHA-256")
                                            .digest(json.writeValueAsBytes(command))));
        } catch (java.security.GeneralSecurityException | java.io.IOException ex) {
            throw new PreviewFailure("COMMAND_INVALID");
        }
        try (var client = manager.client(node.connection())) {
            Receipt receipt;
            try {
                receipt = client.apply(command, deadline);
            } catch (PreviewFailure unknown) {
                // A single bounded read may recover a lost creation response; never replay CREATE.
                if (action != Action.CREATE) throw unknown;
                receipt =
                        client.get("operations/" + command.operationId(), Receipt.class, deadline);
            }
            if (!command.operationId().equals(receipt.operationId()))
                throw new PreviewFailure("NODE_INVALID_RESPONSE");
            if (!java.util.Set.of("SUCCEEDED", "FAILED", "PENDING", "UNKNOWN")
                            .contains(receipt.state())
                    || receipt.reasonCode() != null
                            && !receipt.reasonCode().matches("[A-Z][A-Z0-9_]{0,79}"))
                throw new PreviewFailure("NODE_INVALID_RESPONSE");
            repository.receipt(receipt);
            if (!receipt.state().equals("SUCCEEDED"))
                throw new PreviewFailure(
                        receipt.reasonCode() == null
                                ? "NODE_OPERATION_PENDING"
                                : receipt.reasonCode());
            return receipt;
        } catch (PreviewFailure failure) {
            manager.invalidate(viewer.nodeId());
            throw failure;
        }
    }

    private PreviewNodeRepository.Node requireNode(String id) {
        var viewer = repository.viewer(id);
        var node = viewer == null ? null : repository.node(viewer.nodeId());
        if (node == null || node.local()) throw new PreviewFailure("NODE_NOT_REGISTERED");
        return node;
    }

    private PreviewNodeRepository.Node requireNode() {
        var node = repository.node();
        if (node == null) throw new PreviewFailure("NODE_NOT_REGISTERED");
        return node;
    }

    private record PendingSource(SourceKey key, URI uri) {
        @Override
        public String toString() {
            return "PendingSource[redacted]";
        }
    }
}
