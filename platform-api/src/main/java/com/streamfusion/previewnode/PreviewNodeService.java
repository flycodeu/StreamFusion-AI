package com.streamfusion.previewnode;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import com.streamfusion.platform.media.preview.control.PreviewControl.*;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import com.streamfusion.previewnode.PreviewNodeJournal.Operation;
import com.streamfusion.previewnode.PreviewNodeJournal.Viewer;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Duration;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Semaphore;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.locks.ReentrantLock;
import org.slf4j.LoggerFactory;

/** Single-node ownership, finite admission and expiry; no camera auth or node scheduling. */
public final class PreviewNodeService {
    private final String nodeId;
    private final String bootId = UUID.randomUUID().toString();
    private final String configVersion;
    private final int maxSessions;
    private final int maxTranscodes;
    private final PreviewExecutor executor;
    private final PreviewNodeJournal journal;
    private final PreviewNodeSourcePolicy sources;
    private final ObjectMapper json;
    private final Clock clock;
    private final Object admission = new Object();
    // Fixed stripes bound lock storage, and serialize create/renew/stop for each viewer.
    private final ReentrantLock[] locks = new ReentrantLock[128];
    private final Semaphore starts;
    private final PreviewBudget capacity;
    private final Map<String, Integer> cleanupAttempts = new ConcurrentHashMap<>();
    private final Map<String, Long> nextCleanup = new ConcurrentHashMap<>();
    private volatile boolean recovered;
    private final Runnable runtimeCheck;
    private volatile long healthyAt;
    private volatile String healthFailure;
    private final PreviewNodeMaintenance maintenance;

    public PreviewNodeService(
            String nodeId,
            String configVersion,
            int maxSessions,
            int maxTranscodes,
            PreviewExecutor executor,
            PreviewNodeJournal journal,
            PreviewNodeSourcePolicy sources,
            ObjectMapper json,
            Clock clock,
            Runnable runtimeCheck) {
        this(
                nodeId,
                configVersion,
                maxSessions,
                maxTranscodes,
                executor,
                journal,
                sources,
                json,
                clock,
                runtimeCheck,
                PreviewBudget.defaults(maxSessions, maxTranscodes));
    }

    public PreviewNodeService(
            String nodeId,
            String configVersion,
            int maxSessions,
            int maxTranscodes,
            PreviewExecutor executor,
            PreviewNodeJournal journal,
            PreviewNodeSourcePolicy sources,
            ObjectMapper json,
            Clock clock,
            Runnable runtimeCheck,
            PreviewBudget capacity) {
        this.capacity = capacity;
        this.starts = new Semaphore(capacity.maxStarting());
        this.nodeId = nodeId;
        this.configVersion = configVersion;
        this.maxSessions = maxSessions;
        this.maxTranscodes = maxTranscodes;
        this.executor = executor;
        this.journal = journal;
        this.sources = sources;
        this.json = json;
        this.clock = clock;
        this.runtimeCheck = runtimeCheck;
        this.maintenance = new PreviewNodeMaintenance(journal.directory(), json, nodeId);
        for (int i = 0; i < locks.length; i++) locks[i] = new ReentrantLock();
    }

    public Snapshot snapshot() {
        return new Snapshot(
                PreviewControl.VERSION,
                nodeId,
                bootId,
                "preview-control-1",
                configVersion,
                !recovered
                        ? "RECONCILING"
                        : healthFailure != null
                                        || executor.protection() != null
                                                && executor.protection().reasonCode() != null
                                        || clock.millis() - healthyAt > 15000
                                ? "DEGRADED"
                                : maintenanceState().state().equals("OPEN")
                                        ? "READY"
                                        : "MAINTENANCE",
                maxSessions,
                maxTranscodes,
                executor.occupiedTranscodes(),
                clock.millis(),
                journal.snapshot().viewers().values().stream()
                        .map(Viewer::resource)
                        .filter(r -> !r.state().equals("CLOSED"))
                        .toList(),
                capacity,
                usage(),
                executor.protection());
    }

    public PreviewBudget.Usage usage() {
        var active =
                journal.snapshot().viewers().values().stream()
                        .filter(v -> !v.resource().state().equals("CLOSED"))
                        .toList();
        int count =
                (int)
                        active.stream()
                                .map(Viewer::source)
                                .filter(java.util.Objects::nonNull)
                                .distinct()
                                .count();
        int transcodes =
                (int)
                        active.stream()
                                .filter(this::potential)
                                .map(Viewer::source)
                                .filter(java.util.Objects::nonNull)
                                .distinct()
                                .count();
        int starting = (int) active.stream().filter(v -> v.playback() == null).count();
        return new PreviewBudget.Usage(
                count,
                active.size(),
                transcodes,
                starting,
                count * capacity.ingressPerSource(),
                active.size() * capacity.egressPerViewer());
    }

    private boolean potential(Viewer viewer) {
        return viewer.playback() == null
                ? capacity.maxTranscodes() > 0 && !Boolean.FALSE.equals(viewer.transcodeAllowed())
                : viewer.playback().transcoded();
    }

    public Resource resource(String id) {
        validId(id);
        var viewer = journal.viewer(id);
        return viewer == null
                ? new Resource(id, bootId, 1, recovered ? "ABSENT" : "UNKNOWN", 0, null)
                : viewer.resource();
    }

    public Receipt query(String id) {
        validId(id);
        var operation = journal.operation(id);
        return operation == null
                ? new Receipt(id, "UNKNOWN", "OPERATION_NOT_FOUND", null)
                : operation.receipt();
    }

    public Receipt execute(Command command) {
        validate(command);
        String digest = digest(command);
        var lock = locks[Math.floorMod(command.resourceId().hashCode(), locks.length)];
        boolean acquired = false;
        try {
            acquired =
                    lock.tryLock(
                            Math.max(1, command.deadline() - clock.millis()),
                            TimeUnit.MILLISECONDS);
            if (!acquired) throw new PreviewFailure("NODE_BUSY");
            var prior = journal.operation(command.operationId());
            if (prior != null) {
                if (!prior.digest().equals(digest)) throw new PreviewFailure("OPERATION_CONFLICT");
                return prior.receipt();
            }
            var deadline =
                    PreviewDeadline.after(
                            Duration.ofMillis(Math.max(1, command.deadline() - clock.millis())));
            if (clock.millis() >= command.deadline()) throw new PreviewFailure("COMMAND_EXPIRED");
            // The receipt is durable before any effect. Interrupted effects remain PENDING.
            journal.save(
                    command.operationId(),
                    new Operation(
                            digest,
                            command.deadline(),
                            new Receipt(command.operationId(), "PENDING", null, null)),
                    null,
                    null,
                    clock.millis());
            Receipt receipt;
            boolean existed = journal.viewer(command.resourceId()) != null;
            try {
                var result =
                        switch (command.action()) {
                            case CREATE -> json.valueToTree(create(command, deadline));
                            case RENEW -> {
                                renew(command.resourceId(), deadline);
                                yield null;
                            }
                            case ANSWER -> {
                                requireLive(command.resourceId());
                                yield json.valueToTree(
                                        executor.answer(
                                                command.resourceId(), command.sdp(), deadline));
                            }
                            case STOP -> {
                                stop(command.resourceId(), deadline);
                                yield null;
                            }
                        };
                deadline.check();
                receipt = new Receipt(command.operationId(), "SUCCEEDED", null, result);
            } catch (RuntimeException failure) {
                String reason =
                        failure instanceof PreviewFailure known
                                ? known.reason()
                                : "NODE_EXECUTION_FAILED";
                if (command.action() == Action.CREATE && !existed
                        || command.action() == Action.ANSWER) closing(command.resourceId(), reason);
                receipt = new Receipt(command.operationId(), "FAILED", reason, null);
            }
            // SDP is ephemeral; a restarted node never resumes the old browser session.
            var persisted =
                    command.action() == Action.ANSWER
                            ? new Receipt(
                                    receipt.operationId(),
                                    receipt.state(),
                                    receipt.reasonCode(),
                                    null)
                            : receipt;
            journal.save(
                    command.operationId(),
                    new Operation(digest, command.deadline(), persisted),
                    null,
                    null,
                    clock.millis());
            LoggerFactory.getLogger(getClass())
                    .info(
                            "Preview node operation nodeId={} operationId={} resourceId={} action={} state={} reasonCode={}",
                            nodeId,
                            command.operationId(),
                            command.resourceId(),
                            command.action(),
                            receipt.state(),
                            receipt.reasonCode());
            return receipt;
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } finally {
            if (acquired) lock.unlock();
        }
    }

    private PreviewExecutor.Playback create(Command command, PreviewDeadline deadline) {
        if (!recovered) throw new PreviewFailure("NODE_RECONCILING");
        if (healthFailure != null
                || executor.protection() != null && executor.protection().reasonCode() != null
                || clock.millis() - healthyAt > 15000)
            throw new PreviewFailure("NODE_MEDIA_UNAVAILABLE");
        executor.requireAdmission();
        var uri = command.sourceUrl() == null ? null : sources.require(command.sourceUrl());
        if (!starts.tryAcquire()) throw new PreviewFailure("NODE_BUSY");
        try {
            synchronized (admission) {
                if (maintenance.sealed()) throw new PreviewFailure("NODE_MAINTENANCE");
                if (journal.viewer(command.resourceId()) != null)
                    throw new PreviewFailure("RESOURCE_CONFLICT");
                var active =
                        journal.snapshot().viewers().values().stream()
                                .filter(v -> !v.resource().state().equals("CLOSED"))
                                .toList();
                boolean existingSource =
                        active.stream().anyMatch(v -> command.source().equals(v.source()));
                boolean existingOutput =
                        active.stream()
                                .anyMatch(v -> command.source().equals(v.source()) && potential(v));
                if (uri == null && !existingSource)
                    throw new PreviewFailure("SOURCE_REUSE_UNAVAILABLE");
                capacity.admit(
                        usage(),
                        !existingSource,
                        capacity.maxTranscodes() > 0
                                && !Boolean.FALSE.equals(command.transcodeAllowed())
                                && !existingOutput);
                if (active.stream()
                        .anyMatch(
                                v ->
                                        v.source() != null
                                                && v.source()
                                                        .profileId()
                                                        .equals(command.source().profileId())
                                                && !v.source().equals(command.source())))
                    throw new PreviewFailure("SOURCE_GENERATION_PENDING");
                save(
                        new Viewer(
                                new Resource(
                                        command.resourceId(),
                                        bootId,
                                        1,
                                        "STARTING",
                                        clock.millis() + 30000,
                                        null),
                                command.source(),
                                null,
                                command.transcodeAllowed()));
            }
            executor.requireRecovered();
            executor.prepare(command.resourceId());
            executor.allowTranscoding(
                    command.resourceId(),
                    capacity.maxTranscodes() > 0
                            && !Boolean.FALSE.equals(command.transcodeAllowed()));
            executor.attachSource(
                    command.resourceId(),
                    command.source(),
                    () -> {
                        if (uri == null) throw new PreviewFailure("SOURCE_REUSE_UNAVAILABLE");
                        return uri;
                    },
                    deadline::check,
                    deadline);
            var result =
                    executor.start(
                            command.resourceId(), command.videoCodecs(), deadline::check, deadline);
            deadline.check();
            executor.confirmReady(command.resourceId(), deadline);
            var old = journal.viewer(command.resourceId());
            save(
                    new Viewer(
                            new Resource(
                                    command.resourceId(),
                                    bootId,
                                    1,
                                    "READY",
                                    old.resource().expiresAt(),
                                    null),
                            old.source(),
                            result,
                            old.transcodeAllowed()));
            return result;
        } finally {
            starts.release();
        }
    }

    public com.streamfusion.platform.media.preview.control.PreviewMaintenance.State
            maintenanceState() {
        synchronized (admission) {
            return maintenance.state();
        }
    }

    public com.streamfusion.platform.media.preview.control.PreviewMaintenance.State maintain(
            com.streamfusion.platform.media.preview.control.PreviewMaintenance.Command command) {
        synchronized (admission) {
            var result =
                    maintenance.apply(
                            command,
                            bootId,
                            configVersion,
                            clock.millis(),
                            () -> {
                                executor.requireRecovered();
                                if (!recovered
                                        || healthFailure != null
                                        || clock.millis() - healthyAt > 15000
                                        || executor.occupiedTranscodes() != 0
                                        || journal.snapshot().viewers().values().stream()
                                                .anyMatch(
                                                        v -> !v.resource().state().equals("CLOSED"))
                                        || journal.snapshot().operations().values().stream()
                                                .anyMatch(
                                                        o -> o.receipt().state().equals("PENDING")))
                                    throw new PreviewFailure("NODE_DRAIN_REQUIRED");
                            });
            LoggerFactory.getLogger(getClass())
                    .info(
                            "Preview maintenance nodeId={} permitId={} jobId={} action={} state={}",
                            nodeId,
                            command.permitId(),
                            command.jobId(),
                            command.action(),
                            result.state());
            return result;
        }
    }

    private void renew(String id, PreviewDeadline deadline) {
        var old = requireLive(id);
        if (!executor.missingSourceViewers(id, deadline).isEmpty()) {
            closing(id, "MEDIA_NOT_READY");
            throw new PreviewFailure("MEDIA_NOT_READY");
        }
        save(
                new Viewer(
                        new Resource(id, bootId, 1, "READY", clock.millis() + 90000, null),
                        old.source(),
                        old.playback(),
                        old.transcodeAllowed()));
    }

    public com.streamfusion.platform.media.preview.PreviewModels.Stats stats(String id) {
        validId(id);
        var lock = locks[Math.floorMod(id.hashCode(), locks.length)];
        if (!lock.tryLock()) throw new PreviewFailure("NODE_BUSY");
        try {
            requireLive(id);
            return executor.stats(id, PreviewDeadline.after(Duration.ofSeconds(3)));
        } finally {
            lock.unlock();
        }
    }

    private Viewer requireLive(String id) {
        var viewer = journal.viewer(id);
        if (viewer == null
                || !viewer.resource().bootId().equals(bootId)
                || !viewer.resource().state().equals("READY")
                || viewer.resource().expiresAt() <= clock.millis())
            throw new PreviewFailure("LEASE_EXPIRED");
        return viewer;
    }

    private void closing(String id, String reason) {
        var old = journal.viewer(id);
        if (old != null && !old.resource().state().equals("CLOSED"))
            save(
                    new Viewer(
                            new Resource(
                                    id,
                                    old.resource().bootId(),
                                    1,
                                    "CLOSING",
                                    old.resource().expiresAt(),
                                    reason),
                            old.source(),
                            old.playback(),
                            old.transcodeAllowed()));
    }

    private void stop(String id, PreviewDeadline deadline) {
        var old = journal.viewer(id);
        if (old != null && old.resource().state().equals("CLOSED")) return;
        if (old != null && !old.resource().bootId().equals(bootId))
            throw new PreviewFailure("NODE_RECONCILING");
        closing(id, "CLEANUP_PENDING");
        executor.close(id, deadline);
        save(
                new Viewer(
                        new Resource(id, bootId, 1, "CLOSED", clock.millis(), null),
                        old == null ? null : old.source(),
                        old == null ? null : old.playback(),
                        old == null ? null : old.transcodeAllowed()));
        cleanupAttempts.remove(id);
        nextCleanup.remove(id);
    }

    private void save(Viewer viewer) {
        journal.save(null, null, viewer.resource().resourceId(), viewer, clock.millis());
    }

    /**
     * Each tick has a finite recovery budget. Failed cleanup remains occupied after six attempts.
     */
    public void tick() {
        try {
            runtimeCheck.run();
            healthyAt = clock.millis();
            healthFailure = null;
        } catch (RuntimeException failure) {
            healthFailure =
                    failure instanceof PreviewFailure known
                            ? known.reason()
                            : "NODE_MEDIA_UNAVAILABLE";
        }
        executor.reconcile(() -> false);
        if (!recovered) {
            executor.requireRecovered();
            if (executor.occupiedTranscodes() != 0) throw new PreviewFailure("NODE_RECONCILING");
            for (var viewer : journal.snapshot().viewers().values())
                if (!viewer.resource().bootId().equals(bootId)
                        && !viewer.resource().state().equals("CLOSED"))
                    save(
                            new Viewer(
                                    new Resource(
                                            viewer.resource().resourceId(),
                                            viewer.resource().bootId(),
                                            1,
                                            "CLOSED",
                                            clock.millis(),
                                            "NODE_RESTARTED"),
                                    viewer.source()));
            for (var entry : journal.snapshot().operations().entrySet()) {
                var old = entry.getValue();
                if (old.receipt().state().equals("PENDING"))
                    journal.save(
                            entry.getKey(),
                            new Operation(
                                    old.digest(),
                                    old.deadline(),
                                    new Receipt(entry.getKey(), "FAILED", "NODE_RESTARTED", null)),
                            null,
                            null,
                            clock.millis());
            }
            recovered = true;
        }
        var budget = PreviewDeadline.after(Duration.ofSeconds(3));
        int processed = 0;
        for (var viewer : journal.snapshot().viewers().values()) {
            var resource = viewer.resource();
            if (resource.state().equals("CLOSED")
                    || resource.expiresAt() > clock.millis() && !resource.state().equals("CLOSING"))
                continue;
            if (processed >= 8) break;
            var lock = locks[Math.floorMod(resource.resourceId().hashCode(), locks.length)];
            if (!lock.tryLock()) continue;
            try {
                var current = journal.viewer(resource.resourceId()).resource();
                if (current.state().equals("CLOSED")
                        || current.expiresAt() > clock.millis()
                                && !current.state().equals("CLOSING")) continue;
                if (cleanupAttempts.getOrDefault(resource.resourceId(), 0) >= 6) continue;
                if (nextCleanup.getOrDefault(resource.resourceId(), 0L) > clock.millis()) continue;
                processed++;
                nextCleanup.put(resource.resourceId(), clock.millis() + 10000);
                cleanupAttempts.merge(resource.resourceId(), 1, Integer::sum);
                stop(resource.resourceId(), budget);
            } catch (PreviewFailure pending) {
                closing(resource.resourceId(), pending.reason());
            } finally {
                lock.unlock();
            }
            if (budget.remaining().toMillis() < 100) break;
        }
    }

    private void validate(Command c) {
        if (c == null
                || c.protocolVersion() != PreviewControl.VERSION
                || !nodeId.equals(c.nodeId())
                || !bootId.equals(c.bootId())
                || !configVersion.equals(c.configVersion())
                || c.generation() != 1) throw new PreviewFailure("NODE_IDENTITY_CHANGED");
        validId(c.operationId());
        validId(c.resourceId());
        if (c.traceId() == null
                || !c.traceId().matches("[A-Za-z0-9_-]{1,64}")
                || c.action() == null
                || c.deadline() <= clock.millis()
                || c.deadline() > clock.millis() + 20000)
            throw new PreviewFailure("COMMAND_EXPIRED");
        if (c.action() == Action.CREATE
                && (c.source() == null
                        || c.source().profileId() == null
                        || c.source().profileId() < 1
                        || c.source().connectionKey() == null
                        || c.source().connectionKey().length() > 256
                        || c.videoCodecs() == null
                        || c.videoCodecs().isEmpty()
                        || c.videoCodecs().size() > 2
                        || !List.of("H264", "H265").containsAll(c.videoCodecs())))
            throw new PreviewFailure("COMMAND_INVALID");
        if (c.action() == Action.ANSWER
                && (c.sdp() == null || c.sdp().length() > 65536 || !c.sdp().startsWith("v=0")))
            throw new PreviewFailure("COMMAND_INVALID");
    }

    private static void validId(String id) {
        if (id == null
                || !id.matches("[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"))
            throw new PreviewFailure("COMMAND_INVALID");
    }

    private String digest(Command command) {
        try {
            return HexFormat.of()
                    .formatHex(
                            MessageDigest.getInstance("SHA-256")
                                    .digest(json.writeValueAsBytes(command)));
        } catch (Exception ex) {
            throw new PreviewFailure("COMMAND_INVALID");
        }
    }
}
