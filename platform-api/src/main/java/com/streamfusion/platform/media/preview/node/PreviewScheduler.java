package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.media.preview.*;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import com.streamfusion.platform.media.preview.execution.LocalPreviewExecutor;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Service;

/** Selects only new sources; an assigned source never moves or fans out to a second node. */
@Service
public final class PreviewScheduler {
    private final PreviewNodeRepository nodes;
    private final PreviewReservationLedger ledger;
    private final PreviewNodeManager manager;
    private final LocalPreviewExecutor local;
    private final ZlmDirectExecutor direct;
    private final String boot = UUID.randomUUID().toString();
    private final Set<String> current = ConcurrentHashMap.newKeySet();
    private final Map<String, String> reasons = new ConcurrentHashMap<>();

    @Autowired
    public PreviewScheduler(
            PreviewNodeRepository nodes,
            PreviewReservationLedger ledger,
            PreviewNodeManager manager,
            LocalPreviewExecutor local,
            ZlmDirectExecutor direct) {
        this.nodes = nodes;
        this.ledger = ledger;
        this.manager = manager;
        this.local = local;
        this.direct = direct;
    }

    public PreviewScheduler(
            PreviewNodeRepository nodes,
            PreviewReservationLedger ledger,
            PreviewNodeManager manager,
            LocalPreviewExecutor local) {
        this(nodes, ledger, manager, local, null);
    }

    public PreviewNodeRepository.Node reserve(
            String id,
            PreviewExecutor.SourceKey key,
            List<String> codecs,
            PreviewDeadline deadline) {
        return reserve(id, key, codecs, deadline, Set.of());
    }

    public PreviewNodeRepository.Node reserve(
            String id,
            PreviewExecutor.SourceKey key,
            List<String> codecs,
            PreviewDeadline deadline,
            Set<String> excluded) {
        List<PreviewNodeRepository.Node> candidates;
        synchronized (manager) {
            var source = ledger.source(key.profileId());
            if (source != null && !source.connectionKey().equals(key.connectionKey()))
                throw new PreviewFailure("SOURCE_GENERATION_PENDING");
            if (source != null && excluded.contains(source.nodeId()))
                throw new PreviewFailure("SOURCE_ASSIGNMENT_CONFLICT");
            candidates =
                    source == null
                            ? nodes.nodes().stream()
                                    .filter(n -> n.mode().equals("REMOTE"))
                                    .filter(n -> n.zlm() == null || codecs.contains("H264"))
                                    .filter(n -> !excluded.contains(n.nodeId()))
                                    .sorted(
                                            Comparator.comparingInt(
                                                            PreviewNodeRepository.Node::priority)
                                                    .thenComparing(
                                                            n ->
                                                                    ledger.usage(n.nodeId())
                                                                                    .viewers()
                                                                            == 0)
                                                    .thenComparing(
                                                            PreviewNodeRepository.Node::nodeId))
                                    .toList()
                            : List.of(Objects.requireNonNull(nodes.node(source.nodeId())));
        }
        String rejection = "NODE_NO_CAPACITY";
        for (var initial : candidates) {
            var candidate = initial;
            deadline.check();
            try {
                String nodeBoot;
                if (candidate.local()) {
                    local.budget(candidate.budget());
                    local.requireAvailable();
                    var configuration = local.configurationVersion();
                    synchronized (manager) {
                        var active = nodes.node("local");
                        if (!active.configVersion().equals(configuration)) {
                            if (!nodes.viewers("local").isEmpty())
                                throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
                            nodes.localConfiguration(configuration, active.version());
                        }
                        candidate = nodes.node("local");
                    }
                    nodeBoot = boot;
                } else if (candidate.zlm() != null) {
                    if (direct == null) throw new PreviewFailure("NODE_UNAVAILABLE");
                    direct.requireAvailable(candidate, deadline.within(Duration.ofSeconds(3)));
                    nodeBoot = "zlm-" + candidate.nodeId().substring(4) + "0000000000000000";
                } else {
                    // Observations are cached for at most 15 seconds; a failed observation
                    // invalidates the cache.
                    var snapshot =
                            manager.observation(candidate, deadline.within(Duration.ofSeconds(3)));
                    requireSnapshot(candidate, snapshot);
                    nodeBoot = snapshot.bootId();
                }
                synchronized (manager) {
                    // Restart or unknown ownership blocks only this node, not healthy peers.
                    if (nodes.viewers(candidate.nodeId()).stream()
                            .anyMatch(v -> !current.contains(v.id())))
                        throw new PreviewFailure("NODE_RECONCILING");
                    var assigned = ledger.source(key.profileId());
                    if (assigned != null && !assigned.nodeId().equals(candidate.nodeId()))
                        return reserveAssigned(id, key, codecs, deadline, assigned.nodeId());
                    ledger.reserve(id, key, codecs, candidate, nodeBoot);
                    current.add(id);
                    reasons.remove(candidate.nodeId());
                    return candidate;
                }
            } catch (PreviewFailure failure) {
                rejection = failure.reason();
                reasons.put(candidate.nodeId(), rejection);
            }
        }
        throw new PreviewFailure(rejection);
    }

    private PreviewNodeRepository.Node reserveAssigned(
            String id,
            PreviewExecutor.SourceKey key,
            List<String> codecs,
            PreviewDeadline deadline,
            String nodeId) {
        // The competing request already checked this node; never perform network work while locked.
        var node = nodes.node(nodeId);
        var cached = manager.cached(nodeId);
        String nodeBoot =
                node.local()
                        ? boot
                        : node.zlm() != null
                                ? "zlm-" + node.nodeId().substring(4) + "0000000000000000"
                                : cached == null ? null : cached.bootId();
        if (nodeBoot == null) throw new PreviewFailure("NODE_RECONCILING");
        if (!node.local() && node.zlm() == null) requireSnapshot(node, cached);
        deadline.check();
        ledger.reserve(id, key, codecs, node, nodeBoot);
        current.add(id);
        return node;
    }

    private void requireSnapshot(
            PreviewNodeRepository.Node node, PreviewControl.Snapshot snapshot) {
        if (!"READY".equals(snapshot.state()))
            throw new PreviewFailure(
                    snapshot.protection() != null && snapshot.protection().reasonCode() != null
                            ? snapshot.protection().reasonCode()
                            : "NODE_RECONCILING");
        if (!node.configVersion().equals(snapshot.configVersion()) || snapshot.budget() == null)
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        var owned =
                nodes.viewers(node.nodeId()).stream()
                        .map(PreviewNodeRepository.Viewer::id)
                        .collect(java.util.stream.Collectors.toSet());
        if (snapshot.resources().stream().anyMatch(r -> !owned.contains(r.resourceId())))
            throw new PreviewFailure("NODE_UNOWNED_RESOURCES");
        // Observed physical output occupancy is an additional guard, never added to matching ledger
        // rows.
        if (snapshot.occupiedTranscodes() > ledger.usage(node.nodeId()).transcodes())
            throw new PreviewFailure("NODE_UNOWNED_RESOURCES");
    }

    public String localState() {
        try {
            local.requireAvailable();
            return nodes.viewers("local").stream().anyMatch(v -> !current.contains(v.id()))
                    ? "RECONCILING"
                    : "READY";
        } catch (PreviewFailure failure) {
            return failure.reason();
        }
    }

    public com.streamfusion.platform.media.preview.execution.PreviewAdmissionProtection.Observation
            localProtection() {
        return local.protection();
    }

    public boolean current(String id) {
        return current.contains(id);
    }

    public String reason(String node) {
        return reasons.get(node);
    }

    public void ready(String id, PreviewExecutor.Playback playback) {
        synchronized (manager) {
            ledger.ready(id, playback);
        }
    }

    public void closed(String id) {
        synchronized (manager) {
            ledger.closed(id);
            current.remove(id);
        }
    }

    public PreviewBudgetView capacity(String node) {
        synchronized (manager) {
            return new PreviewBudgetView(ledger.usage(node), reasons.get(node));
        }
    }

    public record PreviewBudgetView(
            com.streamfusion.platform.media.preview.control.PreviewBudget.Usage usage,
            String reasonCode) {}
}
