package com.streamfusion.platform.media.preview;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import com.streamfusion.platform.media.preview.source.PreviewLocatorService;
import com.streamfusion.platform.system.auth.pojo.dto.SessionPrincipalDto;
import jakarta.annotation.PreDestroy;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.boot.web.context.WebServerApplicationContext;
import org.springframework.context.event.EventListener;
import org.springframework.stereotype.Service;

/** Owns authorized viewer leases and delegates media ownership to a preview executor. */
@Service
public class PreviewLeaseService {
    private static final Logger LOG = LoggerFactory.getLogger(PreviewLeaseService.class);
    private static final Duration TTL = Duration.ofSeconds(90);
    private static final Duration CREATE_TIMEOUT = Duration.ofSeconds(18);
    private static final Duration REQUEST_TIMEOUT = Duration.ofSeconds(10);
    private static final Duration CLEANUP_TIMEOUT = Duration.ofSeconds(12);
    private static final int MAX_LEASES = 64;
    private static final int MAX_PENDING_REQUESTS = 4096;
    private static final int MAX_CLEANUP_ATTEMPTS = 6;
    private final PreviewAuthorization authorization;
    private final PreviewProfileSelector profileSelector;
    private final PreviewLocatorService locators;
    private final PreviewExecutor executor;
    private final Clock clock;
    private final PreviewProperties limits;
    private final java.util.concurrent.Semaphore starts =
            new java.util.concurrent.Semaphore(4, true);
    private final Object guard = new Object();
    private final Map<String, Lease> leases = new HashMap<>();
    private final Map<RequestKey, RequestEntry> requests = new HashMap<>();
    private volatile boolean shuttingDown;
    private volatile boolean recoveryEnabled;
    private String lastRecoveryFailure;
    private final ThreadPoolExecutor cleanupWorkers =
            new ThreadPoolExecutor(
                    4,
                    4,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(MAX_LEASES),
                    r -> {
                        var t = new Thread(r, "preview-cleanup");
                        t.setDaemon(true);
                        return t;
                    },
                    new ThreadPoolExecutor.AbortPolicy());
    private final ScheduledExecutorService reaper =
            Executors.newSingleThreadScheduledExecutor(
                    r -> {
                        var t = new Thread(r, "preview-lease-reaper");
                        t.setDaemon(true);
                        return t;
                    });

    public PreviewLeaseService(
            PreviewAuthorization authorization,
            PreviewProfileSelector profileSelector,
            PreviewLocatorService locators,
            PreviewExecutor executor,
            Clock clock,
            PreviewProperties limits) {
        this.authorization = authorization;
        this.profileSelector = profileSelector;
        this.locators = locators;
        this.executor = executor;
        this.clock = clock;
        this.limits = limits;
        reaper.scheduleWithFixedDelay(this::reapSafely, 1, 10, TimeUnit.SECONDS);
    }

    public PreviewModels.LeaseView create(PreviewModels.CreateLease input) {
        validate(input);
        long cameraId = DecimalInput.id(input.cameraId(), "cameraId");
        var owner = authorization.principal(cameraId);
        var key =
                new RequestKey(owner.getUserId(), input.clientRequestId().toLowerCase(Locale.ROOT));
        Lease lease;
        RequestEntry entry;
        boolean existing;
        synchronized (guard) {
            if (shuttingDown) throw new PreviewFailure("PREVIEW_UNAVAILABLE");
            requests.entrySet()
                    .removeIf(
                            e ->
                                    e.getValue().expiresAt.isBefore(now())
                                            && e.getValue().result.isDone());
            entry = requests.get(key);
            existing = entry != null;
            if (existing) {
                if (!samePayload(entry.input, input))
                    throw BusinessException.error(ErrorCode.CONFLICT);
                lease = null;
            } else {
                if (leases.size() >= executor.sessionLimit(limits.maxSessions()))
                    throw BusinessException.error(ErrorCode.RATE_LIMITED)
                            .diagnosed("PREVIEW_CAPACITY_EXCEEDED");
                if (requests.size() >= MAX_PENDING_REQUESTS)
                    throw BusinessException.error(ErrorCode.RATE_LIMITED);
                String id = UUID.randomUUID().toString();
                lease = new Lease(id, owner, cameraId, input, now().plus(TTL), MDC.get("traceId"));
                leases.put(id, lease);
                entry = new RequestEntry(input, lease.expiresAt, lease.createDeadline);
                requests.put(key, entry);
            }
        }
        if (existing) {
            try {
                var result =
                        entry.result.isDone()
                                ? entry.result.get()
                                : entry.result.get(
                                        entry.deadline.remaining().toNanos(), TimeUnit.NANOSECONDS);
                if (result.leaseId() != null) {
                    var active = owned(result.leaseId());
                    requireEnabled(active);
                    synchronized (active) {
                        checkReady(active);
                        return view(active);
                    }
                }
                return result;
            } catch (InterruptedException ex) {
                Thread.currentThread().interrupt();
                throw new PreviewFailure("CANCELLED");
            } catch (ExecutionException ex) {
                if (ex.getCause() instanceof RuntimeException failure) throw failure;
                throw new PreviewFailure("PREVIEW_UNAVAILABLE");
            } catch (java.util.concurrent.TimeoutException ex) {
                throw new PreviewFailure("PREVIEW_PENDING");
            }
        }
        try {
            var result = initialize(lease);
            entry.result.complete(result);
            return result;
        } catch (BusinessException ex) {
            closeLater(lease);
            entry.result.completeExceptionally(ex);
            throw ex;
        } catch (RuntimeException ex) {
            String reason =
                    ex instanceof PreviewFailure failure ? failure.reason() : "PREVIEW_UNAVAILABLE";
            closeLater(lease);
            var result =
                    PreviewModels.LeaseView.unavailable(
                            input.cameraId(),
                            lease.profileId == null
                                    ? input.streamProfileId()
                                    : lease.profileId.toString(),
                            reason);
            entry.result.complete(result);
            return result;
        }
    }

    public PreviewModels.Capacity capacity() {
        authorization.capacity();
        int occupied;
        int closing;
        java.util.Set<String> activeLeaseIds;
        synchronized (guard) {
            occupied = leases.size();
            activeLeaseIds = java.util.Set.copyOf(leases.keySet());
            // closing is volatile: do not acquire lease monitors while holding guard.
            closing = (int) leases.values().stream().filter(lease -> lease.closing).count();
        }
        int retained = executor.retainedViewers(activeLeaseIds);
        occupied += retained;
        closing += retained;
        Integer occupiedTranscodes;
        try {
            occupiedTranscodes = executor.occupiedTranscodes();
        } catch (PreviewFailure unavailable) {
            occupiedTranscodes = null;
        }
        return new PreviewModels.Capacity(
                executor.sessionLimit(limits.maxSessions()),
                occupied,
                closing,
                executor.transcodeLimit(limits.maxTranscodes()),
                occupiedTranscodes,
                occupiedTranscodes != null,
                now());
    }

    private PreviewModels.LeaseView initialize(Lease lease) {
        var deadline = lease.createDeadline;
        beginOperation(lease, deadline);
        boolean acquired = false;
        try {
            while (!acquired) {
                checkActive(lease, deadline);
                acquired =
                        starts.tryAcquire(
                                Math.min(100, deadline.remaining().toMillis()),
                                TimeUnit.MILLISECONDS);
            }
            return initialize(lease, deadline);
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } finally {
            if (acquired) starts.release();
            endOperation(lease, deadline);
        }
    }

    private PreviewModels.LeaseView initialize(Lease lease, PreviewDeadline deadline) {
        deadline.check();
        executor.requireRecovered();
        var profile =
                profileSelector.select(
                        lease.cameraId, lease.input.streamProfileId(), lease.input.usage());
        if (profile == null) throw new PreviewFailure("PROFILE_NOT_FOUND");
        lease.profileId = profile.getId();
        if (!Boolean.TRUE.equals(profile.getEnabled()))
            throw new PreviewFailure("PROFILE_DISABLED");
        authorization.request(lease.owner, lease.cameraId);
        lease.connectionKey = locators.connectionKey(profile);
        executor.prepare(
                lease.id,
                new PreviewExecutor.SourceKey(profile.getId(), lease.connectionKey),
                lease.input.videoCodecs(),
                deadline);
        executor.attachSource(
                lease.id,
                new PreviewExecutor.SourceKey(profile.getId(), lease.connectionKey),
                () ->
                        locators.resolve(
                                        profile,
                                        () -> {
                                            checkActive(lease, deadline);
                                            authorization.request(lease.owner, lease.cameraId);
                                        },
                                        deadline)
                                .uri(),
                () -> checkActive(lease, deadline),
                deadline);
        checkUpstream(lease, deadline);
        checkActive(lease, deadline);
        authorization.request(lease.owner, lease.cameraId);
        requireEnabled(lease);
        var playback =
                executor.start(
                        lease.id,
                        lease.input.videoCodecs(),
                        () -> checkActive(lease, deadline),
                        deadline);
        deadline.check();
        authorization.request(lease.owner, lease.cameraId);
        requireEnabled(lease);
        synchronized (lease) {
            checkActive(lease, deadline);
            lease.codec = playback.codec();
            lease.transcoded = playback.transcoded();
            executor.confirmReady(lease.id, deadline);
            lease.ready = true;
            lease.expiresAt = now().plus(TTL);
            return view(lease);
        }
    }

    public PreviewModels.LeaseView renew(String id) {
        var lease = owned(id);
        requireEnabled(lease);
        var deadline = PreviewDeadline.after(REQUEST_TIMEOUT);
        beginOperation(lease, deadline);
        try {
            checkUpstream(lease, deadline);
            executor.renew(lease.id, deadline);
            synchronized (lease) {
                checkReady(lease);
                lease.expiresAt = now().plus(TTL);
                return view(lease);
            }
        } finally {
            endOperation(lease, deadline);
        }
    }

    private void checkUpstream(Lease lease, PreviewDeadline deadline) {
        var missing = executor.missingSourceViewers(lease.id, deadline);
        if (missing.isEmpty()) return;
        List<Lease> affected;
        synchronized (guard) {
            affected =
                    missing.stream().map(leases::get).filter(java.util.Objects::nonNull).toList();
        }
        affected.forEach(this::closeLater);
        throw new PreviewFailure("MEDIA_NOT_READY");
    }

    public PreviewModels.Answer answer(String id, PreviewModels.Offer offer) {
        if (offer == null
                || offer.sdp() == null
                || offer.sdp().length() > 65536
                || !offer.sdp().startsWith("v=0"))
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        var lease = owned(id);
        requireEnabled(lease);
        var deadline = PreviewDeadline.after(REQUEST_TIMEOUT);
        synchronized (lease) {
            checkReady(lease);
            if (lease.signalled || lease.signalling)
                throw new PreviewFailure("SIGNAL_ALREADY_CREATED");
            beginOperation(lease, deadline);
            lease.signalling = true;
        }
        try {
            checkUpstream(lease, deadline);
            var answer = executor.answer(lease.id, offer.sdp(), deadline);
            authorization.request(lease.owner, lease.cameraId);
            requireEnabled(lease);
            synchronized (lease) {
                checkActive(lease, deadline);
                lease.signalled = true;
            }
            return answer;
        } catch (RuntimeException ex) {
            closeLater(lease);
            throw ex;
        } finally {
            synchronized (lease) {
                lease.signalling = false;
            }
            endOperation(lease, deadline);
        }
    }

    public PreviewModels.Stats stats(String id) {
        var lease = owned(id);
        requireEnabled(lease);
        var deadline = PreviewDeadline.after(REQUEST_TIMEOUT);
        synchronized (lease) {
            checkReady(lease);
        }
        beginOperation(lease, deadline);
        try {
            var result = executor.stats(lease.id, deadline);
            if (!"READY".equals(result.state())) checkUpstream(lease, deadline);
            authorization.request(lease.owner, lease.cameraId);
            checkActive(lease, deadline);
            return result;
        } catch (RuntimeException ex) {
            if (ex instanceof BusinessException
                    || ex instanceof PreviewFailure failure
                            && Set.of("PREVIEW_TIMEOUT", "CANCELLED", "LEASE_EXPIRED")
                                    .contains(failure.reason())) closeLater(lease);
            throw ex;
        } finally {
            endOperation(lease, deadline);
        }
    }

    public void delete(String id) {
        var lease = find(id);
        if (lease == null) return;
        authorizeOwner(lease);
        release(lease, true);
        if (!lease.cleanupComplete) throw new PreviewFailure("CLEANUP_PENDING");
    }

    private Lease owned(String id) {
        var lease = require(id);
        authorizeOwner(lease);
        return lease;
    }

    private void authorizeOwner(Lease lease) {
        try {
            authorization.request(lease.owner, lease.cameraId);
        } catch (RuntimeException ex) {
            // A different caller cannot terminate an owner's stream; revoked owners are reaped.
            if (!(ex instanceof PreviewFailure)) closeLater(lease);
            throw ex;
        }
    }

    private Lease require(String id) {
        var value = find(id);
        if (value == null || !now().isBefore(value.expiresAt) || value.closing)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        return value;
    }

    private Lease find(String id) {
        if (id == null || !id.matches("[0-9a-f-]{36}"))
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        synchronized (guard) {
            return leases.get(id);
        }
    }

    private void checkReady(Lease lease) {
        if (!lease.ready || lease.closing || !now().isBefore(lease.expiresAt))
            throw new PreviewFailure("LEASE_EXPIRED");
    }

    private void requireEnabled(Lease lease) {
        var profile = profileSelector.enabled(lease.cameraId, lease.profileId);
        if (profile == null) {
            closeLater(lease);
            throw new PreviewFailure("CAMERA_DISABLED");
        }
        try {
            if (lease.connectionKey != null
                    && !lease.connectionKey.equals(locators.connectionKey(profile)))
                throw new PreviewFailure("CONFIGURATION_CHANGED");
            executor.requireCurrent(lease.id);
        } catch (RuntimeException failure) {
            closeLater(lease);
            throw failure;
        }
    }

    private void validate(PreviewModels.CreateLease input) {
        if (input == null
                || input.clientRequestId() == null
                || !input.clientRequestId()
                        .matches(
                                "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}")
                || input.videoCodecs() == null
                || input.videoCodecs().isEmpty()
                || input.videoCodecs().size() > 2
                || input.videoCodecs().stream()
                        .anyMatch(codec -> !"H264".equals(codec) && !"H265".equals(codec))
                || input.usage() != null && !Set.of("MAIN", "SUB").contains(input.usage()))
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }

    private static boolean samePayload(
            PreviewModels.CreateLease first, PreviewModels.CreateLease second) {
        return first.cameraId().equals(second.cameraId())
                && java.util.Objects.equals(first.streamProfileId(), second.streamProfileId())
                && java.util.Objects.equals(first.usage(), second.usage())
                && first.videoCodecs().equals(second.videoCodecs());
    }

    private PreviewModels.LeaseView view(Lease lease) {
        return new PreviewModels.LeaseView(
                lease.id,
                Long.toString(lease.cameraId),
                lease.profileId.toString(),
                "READY",
                null,
                lease.expiresAt,
                lease.codec,
                lease.transcoded);
    }

    private Instant now() {
        return clock.instant();
    }

    private void release(Lease lease) {
        release(lease, false);
    }

    private void release(Lease lease, boolean manual) {
        synchronized (lease) {
            lease.closing = true;
            lease.ready = false;
            lease.operations.forEach(PreviewDeadline::cancel);
            if (!lease.operations.isEmpty()
                    || lease.cleanupComplete
                    || lease.cleanupRunning
                    || lease.nextCleanupAt != null && now().isBefore(lease.nextCleanupAt)
                    || !manual && lease.cleanupAttempts >= MAX_CLEANUP_ATTEMPTS) return;
            lease.cleanupRunning = true;
            lease.cleanupAttempts = Math.min(MAX_CLEANUP_ATTEMPTS, lease.cleanupAttempts + 1);
        }
        try {
            var deadline = PreviewDeadline.after(CLEANUP_TIMEOUT);
            executor.close(lease.id, deadline);
            synchronized (lease) {
                lease.cleanupComplete = true;
            }
            synchronized (guard) {
                leases.remove(lease.id, lease);
            }
        } catch (RuntimeException ex) {
            synchronized (lease) {
                lease.nextCleanupAt = now().plusSeconds(Math.min(60, 5L << lease.cleanupAttempts));
            }
            LOG.warn(
                    "Preview cleanup pending traceId={} leaseId={} attempt={} reasonCode={}",
                    lease.traceId,
                    lease.id,
                    lease.cleanupAttempts,
                    ex instanceof PreviewFailure failure
                            ? failure.reason()
                            : "DEPENDENCY_UNAVAILABLE");
        } finally {
            synchronized (lease) {
                lease.cleanupRunning = false;
            }
        }
    }

    private void closeLater(Lease lease) {
        synchronized (lease) {
            lease.closing = true;
            lease.ready = false;
            lease.operations.forEach(PreviewDeadline::cancel);
        }
        queueCleanup(lease);
    }

    private void beginOperation(Lease lease, PreviewDeadline deadline) {
        synchronized (lease) {
            checkActive(lease, deadline);
            lease.operations.add(deadline);
        }
    }

    private void endOperation(Lease lease, PreviewDeadline deadline) {
        boolean cleanup;
        synchronized (lease) {
            lease.operations.remove(deadline);
            cleanup = lease.closing && lease.operations.isEmpty();
        }
        if (cleanup) {
            if (shuttingDown) release(lease);
            else queueCleanup(lease);
        }
    }

    private void checkActive(Lease lease, PreviewDeadline deadline) {
        deadline.check();
        synchronized (lease) {
            if (lease.closing || !now().isBefore(lease.expiresAt))
                throw new PreviewFailure("LEASE_EXPIRED");
        }
    }

    void reap() {
        sweep(false);
    }

    private void sweep(boolean asynchronous) {
        List<Lease> values;
        synchronized (guard) {
            values = List.copyOf(leases.values());
            requests.entrySet()
                    .removeIf(
                            e ->
                                    e.getValue().expiresAt.isBefore(now())
                                            && e.getValue().result.isDone());
        }
        for (var lease : values) {
            if (lease.closing) {
                if (lease.cleanupAttempts < MAX_CLEANUP_ATTEMPTS
                        && (lease.nextCleanupAt == null || !now().isBefore(lease.nextCleanupAt)))
                    if (asynchronous) queueCleanup(lease);
                    else release(lease);
            } else if (!now().isBefore(lease.expiresAt)
                    || !authorization.stillAllowed(lease.owner, lease.cameraId)) {
                synchronized (lease) {
                    lease.closing = true;
                    lease.operations.forEach(PreviewDeadline::cancel);
                }
                if (asynchronous) queueCleanup(lease);
                else release(lease);
            } else if (lease.profileId != null) {
                try {
                    requireEnabled(lease);
                } catch (RuntimeException invalid) {
                    if (asynchronous) queueCleanup(lease);
                    else release(lease);
                }
            }
        }
    }

    private void queueCleanup(Lease lease) {
        synchronized (lease) {
            if (!lease.operations.isEmpty() || lease.cleanupComplete) return;
        }
        if (!lease.cleanupQueued.compareAndSet(false, true)) return;
        try {
            cleanupWorkers.execute(
                    () -> {
                        try {
                            release(lease);
                        } finally {
                            lease.cleanupQueued.set(false);
                        }
                    });
        } catch (java.util.concurrent.RejectedExecutionException ex) {
            lease.cleanupQueued.set(false);
        }
    }

    private void reapSafely() {
        try {
            if (recoveryEnabled) {
                reconcile();
                lastRecoveryFailure = null;
            }
        } catch (RuntimeException ex) {
            String reason =
                    ex instanceof PreviewFailure known ? known.reason() : "DEPENDENCY_UNAVAILABLE";
            if (!reason.equals(lastRecoveryFailure))
                LOG.warn(
                        "Preview recovery deferred traceId={} reasonCode={}",
                        "preview-recovery",
                        reason);
            lastRecoveryFailure = reason;
        }
        try {
            sweep(true);
        } catch (RuntimeException ex) {
            LOG.warn("Preview lease sweep deferred reasonCode={}", "DEPENDENCY_UNAVAILABLE");
        }
    }

    @EventListener(ApplicationReadyEvent.class)
    public void ready(ApplicationReadyEvent event) {
        recoveryEnabled = event.getApplicationContext() instanceof WebServerApplicationContext;
    }

    void reconcile() {
        executor.reconcile(() -> shuttingDown);
    }

    @PreDestroy
    public void close() {
        reaper.shutdownNow();
        List<Lease> values;
        synchronized (guard) {
            shuttingDown = true;
            values = List.copyOf(leases.values());
        }
        for (var lease : values) {
            synchronized (lease) {
                lease.closing = true;
                lease.operations.forEach(PreviewDeadline::cancel);
            }
        }
        values.forEach(this::queueCleanup);
        cleanupWorkers.shutdown();
        try {
            if (!cleanupWorkers.awaitTermination(15, TimeUnit.SECONDS))
                cleanupWorkers.shutdownNow();
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            cleanupWorkers.shutdownNow();
        }
    }

    private record RequestKey(Long owner, String requestId) {}

    private static final class RequestEntry {
        final PreviewModels.CreateLease input;
        final Instant expiresAt;
        final PreviewDeadline deadline;
        final CompletableFuture<PreviewModels.LeaseView> result = new CompletableFuture<>();

        RequestEntry(PreviewModels.CreateLease input, Instant expiresAt, PreviewDeadline deadline) {
            this.input = input;
            this.expiresAt = expiresAt;
            this.deadline = deadline;
        }
    }

    private static final class Lease {
        final String id;
        final SessionPrincipalDto owner;
        final long cameraId;
        final PreviewModels.CreateLease input;
        final String traceId;
        volatile Instant expiresAt;
        volatile Long profileId;
        volatile String connectionKey;
        boolean ready;
        volatile boolean closing;
        boolean signalled;
        boolean signalling;
        boolean transcoded;
        String codec;
        volatile int cleanupAttempts;
        boolean cleanupRunning;
        volatile boolean cleanupComplete;
        volatile Instant nextCleanupAt;
        final AtomicBoolean cleanupQueued = new AtomicBoolean();
        final Set<PreviewDeadline> operations = new HashSet<>();
        final PreviewDeadline createDeadline = PreviewDeadline.after(CREATE_TIMEOUT);

        Lease(
                String id,
                SessionPrincipalDto owner,
                long cameraId,
                PreviewModels.CreateLease input,
                Instant expiresAt,
                String traceId) {
            this.id = id;
            this.owner = owner;
            this.cameraId = cameraId;
            this.input = input;
            this.expiresAt = expiresAt;
            this.traceId = traceId;
        }
    }
}
