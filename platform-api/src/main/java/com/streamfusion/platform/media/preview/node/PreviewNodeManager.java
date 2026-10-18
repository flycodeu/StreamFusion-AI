package com.streamfusion.platform.media.preview.node;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import jakarta.annotation.PreDestroy;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
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
    private final ZlmDirectGateway zlm;

    private record Observation(PreviewControl.Snapshot snapshot, Instant at, String reason) {}

    private final java.util.Map<String, Observation> observations =
            new java.util.concurrent.ConcurrentHashMap<>();

    private record DirectObservation(
            String version, ZlmDirectGateway.ListenerPorts ports, Instant at, String reason) {}

    private record DirectLoad(
            ZlmDirectConnection connection,
            ZlmDirectGateway.NetworkThreadLoad load,
            Instant checkedAt) {}

    private final java.util.Map<String, DirectObservation> directObservations =
            new java.util.concurrent.ConcurrentHashMap<>();
    private final java.util.Map<String, DirectLoad> directLoads = new ConcurrentHashMap<>();
    private final Set<String> loadPending = new HashSet<>();
    private final Object loadLock = new Object();
    private final Map<String, Long> loadEpochs = new HashMap<>();
    private final ThreadPoolExecutor loadWorkers =
            new ThreadPoolExecutor(
                    2,
                    2,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(16),
                    task -> {
                        var thread = new Thread(task, "preview-zlm-load");
                        thread.setDaemon(true);
                        return thread;
                    },
                    new ThreadPoolExecutor.AbortPolicy());

    public PreviewNodeManager(
            PreviewNodeRepository repository,
            PreviewNodeProperties properties,
            PreviewProperties limits,
            ObjectMapper json) {
        this(repository, properties, limits, json, null);
    }

    @org.springframework.beans.factory.annotation.Autowired
    public PreviewNodeManager(
            PreviewNodeRepository repository,
            PreviewNodeProperties properties,
            PreviewProperties limits,
            ObjectMapper json,
            ZlmDirectGateway zlm) {
        this.repository = repository;
        this.properties = properties;
        this.limits = limits;
        this.json = json;
        this.zlm = zlm;
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

    PreviewNodeRepository.Node administrativeNode(String nodeId) {
        return nodeId == null ? repository.node() : repository.node(nodeId);
    }

    public void attachOpsSource(String nodeId, OpsNodeSource source) {
        if (source == null) return;
        var node = repository.node(nodeId);
        repository.attachOpsSource(nodeId, source, node.version());
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
        return inspect(node, deadline, true);
    }

    private PreviewControl.Snapshot inspect(
            PreviewNodeRepository.Node node, PreviewDeadline deadline, boolean recordObservation) {
        try (var client = client(node.connection())) {
            var value = client.get("snapshot", PreviewControl.Snapshot.class, deadline);
            if (value.protocolVersion() != 1
                    || !node.connection().nodeId().equals(value.nodeId())
                    || value.bootId() == null
                    || value.configVersion() == null
                    || value.resources() == null
                    || value.resources().size() > 64)
                throw new PreviewFailure("NODE_IDENTITY_CHANGED");
            if (recordObservation)
                observations.put(node.nodeId(), new Observation(value, Instant.now(), null));
            return value;
        } catch (PreviewFailure ex) {
            if (recordObservation)
                observations.put(node.nodeId(), new Observation(null, Instant.now(), ex.reason()));
            throw ex;
        }
    }

    public View check() {
        for (var node : repository.nodes()) {
            if (node.local()) continue;
            try {
                if (node.zlm() != null) {
                    inspectDirect(node);
                    observeDirectLoad(node);
                } else inspect(node, PreviewDeadline.after(Duration.ofSeconds(4)));
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
        directObservations.remove(nodeId);
        synchronized (loadLock) {
            // An in-flight sample must not repopulate a cache invalidated by a connection edit.
            if (loadPending.contains(nodeId)) loadEpochs.merge(nodeId, 1L, Long::sum);
            else loadEpochs.remove(nodeId);
            directLoads.remove(nodeId);
        }
    }

    public String inspectDirect(PreviewNodeRepository.Node node) {
        if (node.zlm() == null || zlm == null) throw new PreviewFailure("ZLM_UNAVAILABLE");
        try {
            var deadline = PreviewDeadline.after(Duration.ofSeconds(4));
            var ports = zlm.requireReady(node.zlm(), deadline);
            String version = zlm.version(node.zlm(), deadline);
            directObservations.put(
                    node.nodeId(), new DirectObservation(version, ports, Instant.now(), null));
            return version;
        } catch (PreviewFailure failure) {
            directObservations.put(
                    node.nodeId(),
                    new DirectObservation(null, null, Instant.now(), failure.reason()));
            throw failure;
        }
    }

    private void observeDirectLoad(PreviewNodeRepository.Node node) {
        var previous = directLoads.get(node.nodeId());
        if (previous != null
                && previous.connection().equals(node.zlm())
                && previous.checkedAt().isAfter(Instant.now().minusSeconds(10))) return;
        long epoch;
        synchronized (loadLock) {
            if (!loadPending.add(node.nodeId())) return;
            epoch = loadEpochs.getOrDefault(node.nodeId(), 0L);
        }
        try {
            loadWorkers.execute(
                    () -> {
                        ZlmDirectGateway.NetworkThreadLoad load;
                        try {
                            load =
                                    zlm.networkThreadLoad(
                                            node.zlm(),
                                            PreviewDeadline.after(Duration.ofSeconds(2)));
                        } catch (RuntimeException unavailable) {
                            load = null;
                        }
                        synchronized (loadLock) {
                            if (epoch == loadEpochs.getOrDefault(node.nodeId(), 0L))
                                directLoads.put(
                                        node.nodeId(),
                                        new DirectLoad(node.zlm(), load, Instant.now()));
                            loadPending.remove(node.nodeId());
                            loadEpochs.remove(node.nodeId());
                        }
                    });
        } catch (java.util.concurrent.RejectedExecutionException full) {
            synchronized (loadLock) {
                loadPending.remove(node.nodeId());
                loadEpochs.remove(node.nodeId());
            }
        }
    }

    @PreDestroy
    public void closeLoadWorkers() {
        loadWorkers.shutdownNow();
    }

    public String directVersion(String nodeId) {
        var observed = freshDirect(nodeId);
        return observed == null ? null : observed.version();
    }

    public ZlmDirectGateway.ListenerPorts directPorts(String nodeId) {
        var observed = freshDirect(nodeId);
        return observed == null ? null : observed.ports();
    }

    public Instant directPortsObservedAt(String nodeId) {
        var observed = freshDirect(nodeId);
        return observed == null || observed.ports() == null ? null : observed.at();
    }

    private ZlmDirectGateway.NetworkThreadLoad directLoad(PreviewNodeRepository.Node node) {
        var observed = directLoads.get(node.nodeId());
        return freshDirect(node.nodeId()) == null
                        || observed == null
                        || !observed.connection().equals(node.zlm())
                        || observed.checkedAt().isBefore(Instant.now().minusSeconds(15))
                ? null
                : observed.load();
    }

    private DirectObservation freshDirect(String nodeId) {
        var observed = directObservations.get(nodeId);
        return observed == null
                        || observed.reason() != null
                        || observed.at().isBefore(Instant.now().minusSeconds(15))
                ? null
                : observed;
    }

    public Instant directObservedAt(String nodeId) {
        var observed = directObservations.get(nodeId);
        return observed == null ? null : observed.at();
    }

    public record Registration(
            PreviewNodeConnection connection,
            PreviewControl.Snapshot snapshot,
            Long expectedVersion,
            long checkedAt) {}

    public Registration registration() {
        return registration((String) null);
    }

    public Registration registration(String nodeId) {
        if (properties.connectionFile().isBlank())
            throw new PreviewFailure("NODE_REGISTRATION_MISSING");
        var connection =
                PreviewNodeConnection.readAll(Path.of(properties.connectionFile()), json).stream()
                        .filter(c -> nodeId == null || c.nodeId().equals(nodeId))
                        .findFirst()
                        .orElseThrow(() -> new PreviewFailure("NODE_NOT_REGISTERED"));
        return registration(connection);
    }

    public record ConnectionInput(
            String nodeId,
            String name,
            String controlUri,
            String certificateSha256,
            String mediaAddress,
            Long version) {}

    public boolean directRegistrationAvailable() {
        if (properties.credentialDirectory().isBlank()) return false;
        try {
            Path directory = Path.of(properties.credentialDirectory());
            return directory.isAbsolute()
                    && Files.isDirectory(directory, LinkOption.NOFOLLOW_LINKS);
        } catch (RuntimeException invalid) {
            return false;
        }
    }

    public Registration registration(ConnectionInput input) {
        if (!directRegistrationAvailable())
            throw new PreviewFailure("NODE_CREDENTIAL_DIRECTORY_MISSING");
        if (input == null) throw new PreviewFailure("NODE_REGISTRATION_INVALID");
        PreviewNodeConnection connection;
        try {
            if (input.nodeId() == null || !input.nodeId().matches("[A-Za-z0-9_-]{1,64}"))
                throw new IllegalArgumentException();
            Path directory = Path.of(properties.credentialDirectory()).toRealPath();
            Path token = directory.resolve(input.nodeId() + ".token");
            if (!Files.isRegularFile(token, LinkOption.NOFOLLOW_LINKS)
                    || !token.toRealPath().getParent().equals(directory))
                throw new IllegalArgumentException();
            com.streamfusion.platform.media.preview.control.PreviewControlTls.secret(token);
            connection =
                    new PreviewNodeConnection(
                            input.nodeId(),
                            input.name(),
                            java.net.URI.create(input.controlUri()),
                            input.certificateSha256(),
                            token,
                            input.mediaAddress());
        } catch (Exception invalid) {
            throw new PreviewFailure("NODE_REGISTRATION_INVALID");
        }
        var old = repository.node(connection.nodeId());
        if (old == null
                ? input.version() != null
                : !java.util.Objects.equals(old.version(), input.version()))
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        return registration(connection);
    }

    Registration registration(PreviewNodeConnection connection) {
        var old = repository.node(connection.nodeId());
        if (old != null && "REMOTE".equals(old.mode()))
            throw new PreviewFailure("NODE_DRAIN_REQUIRED");
        var snapshot =
                inspect(
                        new PreviewNodeRepository.Node(connection, "LOCAL", null, 0),
                        PreviewDeadline.after(Duration.ofSeconds(4)),
                        false);
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
        invalidate(candidate.connection().nodeId());
        return view(candidate.connection().nodeId());
    }

    Path credential(String nodeId) {
        var node = repository.node(nodeId);
        return node == null || node.connection() == null ? null : node.connection().tokenFile();
    }

    boolean credentialInUse(Path file) {
        return repository.nodes().stream()
                .anyMatch(n -> n.connection() != null && n.connection().tokenFile().equals(file));
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
                node.local() || node.zlm() != null || !p.mode().equals("REMOTE")
                        ? null
                        : inspect(node, PreviewDeadline.after(Duration.ofSeconds(4)));
        if (node.zlm() != null) {
            if (p.budget().maxTranscodes() != 0) throw new PreviewFailure("NODE_BUDGET_MISMATCH");
            if ("REMOTE".equals(p.mode())) inspectDirect(node);
        }
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
            String kind,
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
            String zlmApiAddress,
            String certificateSha256,
            String mediaAddress,
            String desiredConfigVersion,
            String zlmVersion,
            ZlmDirectGateway.NetworkThreadLoad zlmLoad,
            PreviewControl.Snapshot observation,
            com.streamfusion.platform.media.preview.execution.PreviewAdmissionProtection.Observation
                    protection,
            OpsNodeSource opsSource) {}

    public java.util.List<NodeStatus> statuses(
            com.streamfusion.platform.media.preview.node.PreviewScheduler scheduler) {
        return repository.nodes().stream()
                .map(
                        n -> {
                            var observed = observations.get(n.nodeId());
                            var direct = directObservations.get(n.nodeId());
                            var snapshot = cached(n.nodeId());
                            var capacity = scheduler.capacity(n.nodeId());
                            return new NodeStatus(
                                    n.nodeId(),
                                    n.kind(),
                                    n.local()
                                            ? "本机"
                                            : n.zlm() != null
                                                    ? n.zlm().name()
                                                    : n.connection().name(),
                                    n.mode(),
                                    n.version(),
                                    n.priority(),
                                    n.budget(),
                                    capacity.usage(),
                                    n.local()
                                            ? scheduler.localState()
                                            : n.zlm() != null
                                                    ? direct != null
                                                                    && direct.reason() == null
                                                                    && direct.at()
                                                                            .isAfter(
                                                                                    Instant.now()
                                                                                            .minusSeconds(
                                                                                                    15))
                                                            ? "READY"
                                                            : "UNKNOWN"
                                                    : snapshot == null
                                                            ? "UNKNOWN"
                                                            : snapshot.state(),
                                    capacity.reasonCode() != null
                                            ? capacity.reasonCode()
                                            : n.zlm() != null
                                                    ? direct == null ? null : direct.reason()
                                                    : observed == null ? null : observed.reason(),
                                    n.local()
                                            ? Instant.now()
                                            : n.zlm() != null
                                                    ? direct == null ? null : direct.at()
                                                    : observed == null ? null : observed.at(),
                                    n.connection() == null
                                            ? null
                                            : n.connection().controlUri().toString(),
                                    n.zlm() == null ? null : n.zlm().apiUri().toString(),
                                    n.connection() == null
                                            ? null
                                            : n.connection().certificateSha256(),
                                    n.local()
                                            ? null
                                            : n.zlm() != null
                                                    ? n.zlm().mediaAddress()
                                                    : n.connection().mediaAddress(),
                                    n.configVersion(),
                                    directVersion(n.nodeId()),
                                    n.zlm() == null ? null : directLoad(n),
                                    snapshot,
                                    n.local()
                                            ? scheduler.localProtection()
                                            : snapshot == null ? null : snapshot.protection(),
                                    n.opsSource());
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
