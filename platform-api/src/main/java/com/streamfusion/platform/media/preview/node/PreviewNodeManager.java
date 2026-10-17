package com.streamfusion.platform.media.preview.node;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.HashSet;
import java.util.Set;
import org.springframework.stereotype.Service;

/**
 * Node configuration, safe observations and administrative preflight; selection lives in
 * PreviewScheduler.
 */
@Service
public final class PreviewNodeManager {
    public record View(
            String mode,
            long version,
            String nodeId,
            String name,
            String controlUri,
            String mediaAddress,
            String desiredConfigVersion,
            String state,
            String reasonCode,
            Instant observedAt,
            PreviewControl.Snapshot observation,
            int retainedViewers,
            boolean registrationAvailable) {}

    final Set<String> localViewers = new HashSet<>();
    private final PreviewNodeRepository repository;
    private final PreviewNodeProperties properties;
    private final PreviewProperties limits;
    private final ObjectMapper json;

    private record Observation(PreviewControl.Snapshot snapshot, Instant at, String reason) {}

    private final java.util.Map<String, Observation> observations =
            new java.util.concurrent.ConcurrentHashMap<>();

    public PreviewNodeManager(
            PreviewNodeRepository repository,
            PreviewNodeProperties properties,
            PreviewProperties limits,
            ObjectMapper json) {
        this.repository = repository;
        this.properties = properties;
        this.limits = limits;
        this.json = json;
    }

    public PreviewNodeClient client(PreviewNodeConnection connection) {
        return new PreviewNodeClient(
                connection.controlUri(),
                connection.certificateSha256(),
                connection.tokenFile(),
                json);
    }

    public synchronized void localPrepared(String id) {
        localViewers.add(id);
    }

    public synchronized void localClosed(String id) {
        localViewers.remove(id);
    }

    public synchronized String mode() {
        var node = repository.node();
        return node == null ? "LOCAL" : node.mode();
    }

    public PreviewControl.Snapshot inspect(
            PreviewNodeRepository.Node node, PreviewDeadline deadline) {
        try (var client = client(node.connection())) {
            var value = client.get("snapshot", PreviewControl.Snapshot.class, deadline);
            if (value.protocolVersion() != 1
                    || !node.connection().nodeId().equals(value.nodeId())
                    || value.bootId() == null
                    || value.configVersion() == null
                    || value.resources() == null
                    || value.resources().size() > 64)
                throw new PreviewFailure("NODE_IDENTITY_CHANGED");
            observations.put(node.nodeId(), new Observation(value, Instant.now(), null));
            return value;
        } catch (PreviewFailure ex) {
            observations.put(node.nodeId(), new Observation(null, Instant.now(), ex.reason()));
            throw ex;
        }
    }

    public View check() {
        for (var node : repository.nodes()) {
            if (node.local()) continue;
            try {
                inspect(node, PreviewDeadline.after(Duration.ofSeconds(4)));
            } catch (PreviewFailure ignored) {
            }
        }
        return view();
    }

    public PreviewControl.Snapshot cached(String nodeId) {
        var value = observations.get(nodeId);
        return value != null
                        && value.reason() == null
                        && value.at().isAfter(Instant.now().minusSeconds(15))
                ? value.snapshot()
                : null;
    }

    public PreviewControl.Snapshot observation(
            PreviewNodeRepository.Node node, PreviewDeadline deadline) {
        var value = cached(node.nodeId());
        return value == null ? inspect(node, deadline) : value;
    }

    public void invalidate(String nodeId) {
        observations.remove(nodeId);
    }

    public record Registration(
            PreviewNodeConnection connection,
            PreviewControl.Snapshot snapshot,
            Long expectedVersion,
            long checkedAt) {}

    public Registration registration() {
        return registration(null);
    }

    public Registration registration(String nodeId) {
        if (properties.connectionFile().isBlank())
            throw new PreviewFailure("NODE_REGISTRATION_MISSING");
        var connection =
                PreviewNodeConnection.readAll(Path.of(properties.connectionFile()), json).stream()
                        .filter(c -> nodeId == null || c.nodeId().equals(nodeId))
                        .findFirst()
                        .orElseThrow(() -> new PreviewFailure("NODE_NOT_REGISTERED"));
        var old = repository.node(connection.nodeId());
        var snapshot =
                inspect(
                        new PreviewNodeRepository.Node(connection, "LOCAL", null, 0),
                        PreviewDeadline.after(Duration.ofSeconds(4)));
        requireEmpty(snapshot, true);
        return new Registration(
                connection,
                snapshot,
                old == null ? null : old.version(),
                System.currentTimeMillis());
    }

    public synchronized View register(Registration candidate) {
        var old = repository.node(candidate.connection().nodeId());
        if (System.currentTimeMillis() - candidate.checkedAt() > 5000
                || old != null && old.mode().equals("REMOTE")
                || !repository.viewers(candidate.connection().nodeId()).isEmpty()
                || !java.util.Objects.equals(
                        old == null ? null : old.version(), candidate.expectedVersion()))
            throw new PreviewFailure("NODE_DRAIN_REQUIRED");
        repository.register(
                candidate.connection(),
                candidate.snapshot().configVersion(),
                candidate.expectedVersion());
        var registered = repository.node(candidate.connection().nodeId());
        if (candidate.snapshot().budget() != null
                && !candidate.snapshot().budget().equals(registered.budget()))
            repository.policy(
                    registered.nodeId(),
                    registered.mode(),
                    registered.priority(),
                    candidate.snapshot().budget(),
                    registered.version());
        return view(candidate.connection().nodeId());
    }

    public record ModeChange(
            String mode, long version, PreviewControl.Snapshot snapshot, long checkedAt) {}

    public ModeChange checkMode(String mode, long version) {
        if (repository.nodes().stream().filter(n -> !n.local()).count() > 1)
            throw new PreviewFailure("NODE_USE_PER_NODE_POLICY");
        var node = repository.node();
        if (node == null
                || node.version() != version
                || mode == null
                || !Set.of("LOCAL", "REMOTE", "DRAINING").contains(mode))
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        var snapshot =
                mode.equals("DRAINING")
                        ? null
                        : inspect(node, PreviewDeadline.after(Duration.ofSeconds(4)));
        return new ModeChange(mode, version, snapshot, System.currentTimeMillis());
    }

    public synchronized View changeMode(ModeChange change) {
        var node = repository.node();
        if (node == null
                || node.version() != change.version()
                || System.currentTimeMillis() - change.checkedAt() > 5000)
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        String mode = change.mode();
        if (!node.mode().equals(mode)) {
            if (mode.equals("REMOTE")) {
                if (!localViewers.isEmpty() || !repository.viewers().isEmpty())
                    throw new PreviewFailure("NODE_DRAIN_REQUIRED");
                requireEmpty(change.snapshot());
                if (!change.snapshot().configVersion().equals(node.configVersion())
                        || change.snapshot().maxSessions() != limits.maxSessions()
                        || change.snapshot().maxTranscodes() != limits.maxTranscodes())
                    throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
            } else if (mode.equals("LOCAL")) {
                if (!repository.viewers().isEmpty())
                    throw new PreviewFailure("NODE_DRAIN_REQUIRED");
                requireEmpty(change.snapshot());
            } else if (node.mode().equals("LOCAL"))
                throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
            repository.mode(mode, change.version());
            var local = repository.node("local");
            if (local != null)
                repository.policy(
                        "local",
                        mode.equals("LOCAL") ? "REMOTE" : "LOCAL",
                        0,
                        local.budget(),
                        local.version());
        }
        return view();
    }

    public record RegistrationOption(String nodeId, String name) {}

    public java.util.List<RegistrationOption> registrationOptions() {
        if (properties.connectionFile().isBlank()) return java.util.List.of();
        return PreviewNodeConnection.readAll(Path.of(properties.connectionFile()), json).stream()
                .map(c -> new RegistrationOption(c.nodeId(), c.name()))
                .toList();
    }

    public record Policy(
            String mode,
            int priority,
            com.streamfusion.platform.media.preview.control.PreviewBudget budget,
            long version) {}

    public record PolicyChange(
            String nodeId, Policy policy, PreviewControl.Snapshot snapshot, long checkedAt) {}

    public PolicyChange checkPolicy(String id, Policy p) {
        var node = repository.node(id);
        if (node == null
                || p == null
                || p.budget() == null
                || node.version() != p.version()
                || !Set.of("REMOTE", "LOCAL", "DRAINING").contains(p.mode())
                || p.priority() < 0
                || p.priority() > 10000) throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        var observed =
                node.local() || !p.mode().equals("REMOTE")
                        ? null
                        : inspect(node, PreviewDeadline.after(Duration.ofSeconds(4)));
        if (observed != null) {
            if (!"READY".equals(observed.state())
                    || !node.configVersion().equals(observed.configVersion()))
                throw new PreviewFailure("NODE_RECONCILING");
            var b = observed.budget();
            var desired = p.budget();
            if (b == null
                    || desired.maxSources() > b.maxSources()
                    || desired.maxViewers() > b.maxViewers()
                    || desired.maxTranscodes() > b.maxTranscodes()
                    || desired.maxStarting() > b.maxStarting()
                    || desired.ingressKbps() > b.ingressKbps()
                    || desired.egressKbps() > b.egressKbps()
                    || desired.sourceUpperKbps() != b.sourceUpperKbps()
                    || desired.outputUpperKbps() != b.outputUpperKbps())
                throw new PreviewFailure("NODE_BUDGET_MISMATCH");
        }
        if (node.local()
                && (p.budget().maxViewers() > limits.maxSessions()
                        || p.budget().maxTranscodes() > limits.maxTranscodes()))
            throw new PreviewFailure("NODE_BUDGET_MISMATCH");
        return new PolicyChange(id, p, observed, System.currentTimeMillis());
    }

    public synchronized void policy(PolicyChange change) {
        var node = repository.node(change.nodeId());
        var p = change.policy();
        if (node == null
                || node.version() != p.version()
                || System.currentTimeMillis() - change.checkedAt() > 5000)
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        if (!repository.viewers(node.nodeId()).isEmpty()
                && (node.budget().sourceUpperKbps() != p.budget().sourceUpperKbps()
                        || node.budget().outputUpperKbps() != p.budget().outputUpperKbps()))
            throw new PreviewFailure("NODE_DRAIN_REQUIRED");
        repository.policy(node.nodeId(), p.mode(), p.priority(), p.budget(), p.version());
    }

    public record NodeStatus(
            String nodeId,
            String name,
            String mode,
            long version,
            int priority,
            com.streamfusion.platform.media.preview.control.PreviewBudget budget,
            com.streamfusion.platform.media.preview.control.PreviewBudget.Usage usage,
            String state,
            String reasonCode,
            Instant observedAt,
            String controlUri,
            String mediaAddress,
            String desiredConfigVersion,
            PreviewControl.Snapshot observation,
            com.streamfusion.platform.media.preview.execution.PreviewAdmissionProtection.Observation
                    protection) {}

    public java.util.List<NodeStatus> statuses(
            com.streamfusion.platform.media.preview.node.PreviewScheduler scheduler) {
        return repository.nodes().stream()
                .map(
                        n -> {
                            var observed = observations.get(n.nodeId());
                            var snapshot = cached(n.nodeId());
                            var capacity = scheduler.capacity(n.nodeId());
                            return new NodeStatus(
                                    n.nodeId(),
                                    n.local() ? "本机" : n.connection().name(),
                                    n.mode(),
                                    n.version(),
                                    n.priority(),
                                    n.budget(),
                                    capacity.usage(),
                                    n.local()
                                            ? scheduler.localState()
                                            : snapshot == null ? "UNKNOWN" : snapshot.state(),
                                    capacity.reasonCode() != null
                                            ? capacity.reasonCode()
                                            : observed == null ? null : observed.reason(),
                                    n.local()
                                            ? Instant.now()
                                            : observed == null ? null : observed.at(),
                                    n.local() ? null : n.connection().controlUri().toString(),
                                    n.local() ? null : n.connection().mediaAddress(),
                                    n.configVersion(),
                                    snapshot,
                                    n.local()
                                            ? scheduler.localProtection()
                                            : snapshot == null ? null : snapshot.protection());
                        })
                .toList();
    }

    private void requireEmpty(PreviewControl.Snapshot snapshot) {
        requireEmpty(snapshot, false);
    }

    private void requireEmpty(PreviewControl.Snapshot snapshot, boolean registration) {
        if (!("READY".equals(snapshot.state())
                        || registration && "MAINTENANCE".equals(snapshot.state()))
                || !snapshot.resources().isEmpty()
                || snapshot.occupiedTranscodes() != 0)
            throw new PreviewFailure("NODE_DRAIN_REQUIRED");
    }

    public synchronized View view() {
        return view(null);
    }

    private View view(String nodeId) {
        var node = nodeId == null ? repository.node() : repository.node(nodeId);
        var entry = node == null ? null : observations.get(node.nodeId());
        var observation = entry == null ? null : entry.snapshot();
        var observedAt = entry == null ? null : entry.at();
        var reason = entry == null ? null : entry.reason();
        boolean fresh = observedAt != null && observedAt.isAfter(Instant.now().minusSeconds(15));
        return new View(
                node == null ? "LOCAL" : node.mode(),
                node == null ? -1 : node.version(),
                node == null ? null : node.connection().nodeId(),
                node == null ? null : node.connection().name(),
                node == null ? null : node.connection().controlUri().toString(),
                node == null ? null : node.connection().mediaAddress(),
                node == null ? null : node.configVersion(),
                reason != null ? "UNREACHABLE" : !fresh ? "UNKNOWN" : observation.state(),
                reason,
                observedAt,
                observation,
                repository.viewers().size(),
                !properties.connectionFile().isBlank());
    }
}
