package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.adapter.HikPlatformApi;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.status.CameraProbeResult.Outcome;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.Semaphore;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicLong;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;

/**
 * Internal execution only; the caller owns the probe, authorization and persisted check identity.
 */
public final class CameraStatusWorker implements AutoCloseable {
    private static final Logger LOG = LoggerFactory.getLogger(CameraStatusWorker.class);
    private final RtspDescribeProbe probe;
    private final CameraMediaProbe mediaProbe;
    private final CameraPlatformProbe platformProbe;
    private final ThreadPoolExecutor executor;
    private final Semaphore capacity;
    private final long startIntervalNanos;
    private final Map<Long, Work> submitted = new ConcurrentHashMap<>();
    private final Map<Long, Work> sources = new ConcurrentHashMap<>();
    private final Map<Long, Work> profiles = new ConcurrentHashMap<>();
    private final AtomicLong nextStart = new AtomicLong();
    private volatile boolean closed;

    public record Limits(int parallelism, int queueCapacity, Duration startInterval) {
        public Limits {
            Objects.requireNonNull(startInterval);
            if (parallelism < 1
                    || parallelism > 32
                    || queueCapacity < 1
                    || queueCapacity > 128
                    || startInterval.isNegative()
                    || startInterval.compareTo(Duration.ofMinutes(5)) > 0)
                throw new IllegalArgumentException("Invalid worker limits");
        }
    }

    @FunctionalInterface
    public interface TargetResolver {
        RtspDescribeProbe.Target resolve(long deadlineNanos, Runnable checkActive);
    }

    public record Request(
            long checkId,
            long sourceId,
            long streamProfileId,
            String traceId,
            RtspDescribeProbe.Target target,
            Duration timeout,
            Runnable beforeProbe,
            TargetResolver targetResolver,
            CameraStatusLayer layer,
            CameraProfileEntity mediaProfile,
            Runnable authorization) {
        public static Request media(
                long checkId,
                long sourceId,
                long streamProfileId,
                String traceId,
                CameraProfileEntity profile,
                Duration timeout,
                Runnable beforeProbe,
                Runnable authorization) {
            return new Request(
                    checkId,
                    sourceId,
                    streamProfileId,
                    traceId,
                    null,
                    timeout,
                    beforeProbe,
                    null,
                    CameraStatusLayer.MEDIA,
                    Objects.requireNonNull(profile),
                    Objects.requireNonNull(authorization));
        }

        public static Request upstream(
                long checkId,
                long sourceId,
                long profileId,
                String traceId,
                CameraProfileEntity profile,
                Duration timeout,
                Runnable beforeProbe,
                Runnable authorization) {
            return new Request(
                    checkId,
                    sourceId,
                    profileId,
                    traceId,
                    null,
                    timeout,
                    beforeProbe,
                    null,
                    CameraStatusLayer.UPSTREAM,
                    profile,
                    authorization);
        }

        public Request(
                long checkId,
                long sourceId,
                long streamProfileId,
                String traceId,
                RtspDescribeProbe.Target target,
                Duration timeout,
                Runnable beforeProbe,
                TargetResolver targetResolver) {
            this(
                    checkId,
                    sourceId,
                    streamProfileId,
                    traceId,
                    target,
                    timeout,
                    beforeProbe,
                    targetResolver,
                    CameraStatusLayer.CONTROL,
                    null,
                    target.authorizationCheck());
        }

        public Request(
                long checkId,
                long sourceId,
                long streamProfileId,
                String traceId,
                RtspDescribeProbe.Target target,
                Duration timeout,
                Runnable beforeProbe) {
            this(checkId, sourceId, streamProfileId, traceId, target, timeout, beforeProbe, null);
        }

        public Request(
                long checkId,
                long sourceId,
                long streamProfileId,
                String traceId,
                RtspDescribeProbe.Target target,
                Duration timeout) {
            this(checkId, sourceId, streamProfileId, traceId, target, timeout, () -> {});
        }

        public Request {
            Objects.requireNonNull(layer);
            if (layer == CameraStatusLayer.CONTROL) Objects.requireNonNull(target);
            else if (mediaProfile == null
                    || target != null
                    || targetResolver != null
                    || !Objects.equals(mediaProfile.getId(), streamProfileId)
                    || !Objects.equals(mediaProfile.getSourceId(), sourceId))
                throw new IllegalArgumentException("Invalid media target");
            Objects.requireNonNull(authorization);
            Objects.requireNonNull(timeout);
            Objects.requireNonNull(beforeProbe);
            if (checkId < 1
                    || sourceId < 1
                    || streamProfileId < 1
                    || traceId == null
                    || !traceId.matches("[A-Za-z0-9_-]{1,128}")
                    || timeout.isNegative()
                    || timeout.isZero()
                    || timeout.compareTo(Duration.ofMinutes(5)) > 0)
                throw new IllegalArgumentException("Invalid check request");
        }

        private boolean sameInput(Request other) {
            return checkId == other.checkId
                    && sourceId == other.sourceId
                    && streamProfileId == other.streamProfileId
                    && traceId.equals(other.traceId)
                    && timeout.equals(other.timeout)
                    && layer.equals(other.layer)
                    && (target == null ? other.target == null : target.sameConnection(other.target))
                    && (mediaProfile == null
                            ? other.mediaProfile == null
                            : other.mediaProfile != null
                                    && Objects.equals(
                                            mediaProfile.getVersion(),
                                            other.mediaProfile.getVersion()))
                    && Objects.equals(targetResolver, other.targetResolver);
        }

        @Override
        public String toString() {
            return "Request[checkId="
                    + checkId
                    + ",sourceId="
                    + sourceId
                    + ",streamProfileId="
                    + streamProfileId
                    + ",traceId="
                    + traceId
                    + ",target=Target[redacted],timeout="
                    + timeout
                    + "]";
        }
    }

    public record Submission(
            boolean accepted, String reasonCode, CompletableFuture<CameraProbeResult> completion) {}

    public CameraStatusWorker(RtspDescribeProbe probe, CameraMediaProbe mediaProbe, Limits limits) {
        this(probe, mediaProbe, null, limits);
    }

    public CameraStatusWorker(
            RtspDescribeProbe probe,
            CameraMediaProbe mediaProbe,
            CameraPlatformProbe platformProbe,
            Limits limits) {
        this.probe = Objects.requireNonNull(probe);
        this.mediaProbe = Objects.requireNonNull(mediaProbe);
        this.platformProbe = platformProbe;
        Objects.requireNonNull(limits);
        startIntervalNanos = limits.startInterval().toNanos();
        nextStart.set(System.nanoTime());
        capacity = new Semaphore(limits.parallelism() + limits.queueCapacity());
        executor =
                new ThreadPoolExecutor(
                        limits.parallelism(),
                        limits.parallelism(),
                        0,
                        TimeUnit.SECONDS,
                        new ArrayBlockingQueue<>(limits.queueCapacity()),
                        runnable -> {
                            var thread = new Thread(runnable, "camera-status");
                            thread.setDaemon(true);
                            return thread;
                        },
                        new ThreadPoolExecutor.AbortPolicy());
    }

    public synchronized Submission trySubmit(Request request) {
        Objects.requireNonNull(request);
        if (closed) return rejected("WORKER_STOPPED");
        if (request.layer() == CameraStatusLayer.MEDIA) mediaProbe.requestRecovery();
        Work previous = submitted.get(request.checkId());
        if (previous != null) return replay(previous, request);
        if (!capacity.tryAcquire()) return rejected("WORKER_BUSY");
        var work = new Work(request);
        submitted.put(request.checkId(), work);
        try {
            executor.execute(work);
            return new Submission(true, null, work.completion);
        } catch (RejectedExecutionException ex) {
            String reason = closed ? "WORKER_STOPPED" : "WORKER_BUSY";
            work.finish(local(reason));
            return new Submission(false, reason, work.completion);
        }
    }

    public boolean cancel(long checkId) {
        Work work = submitted.get(checkId);
        if (work == null) return false;
        if (!work.requestCancellation()) return false;
        if (executor.remove(work)) work.finish(cancelled());
        return true;
    }

    /** One source request consumes one worker slot; members retain independent check identities. */
    public synchronized List<Submission> trySubmitBatch(List<Request> requests) {
        if (requests.isEmpty()
                || requests.size() > HikPlatformApi.STATUS_BATCH_SIZE
                || requests.stream().anyMatch(r -> r.layer() != CameraStatusLayer.UPSTREAM)
                || requests.stream().map(Request::sourceId).distinct().count() != 1
                || requests.stream().map(Request::checkId).distinct().count() != requests.size()
                || requests.stream().map(Request::streamProfileId).distinct().count()
                        != requests.size())
            throw new IllegalArgumentException("Invalid upstream batch");
        String reason =
                closed
                        ? "WORKER_STOPPED"
                        : requests.stream().anyMatch(r -> submitted.containsKey(r.checkId()))
                                ? "CHECK_ID_CONFLICT"
                                : null;
        if (reason == null && !capacity.tryAcquire()) reason = "WORKER_BUSY";
        if (reason != null) {
            String rejectedReason = reason;
            return requests.stream().map(r -> rejected(rejectedReason)).toList();
        }
        var batch = new BatchWork(requests);
        batch.members.forEach(w -> submitted.put(w.request.checkId(), w));
        try {
            executor.execute(batch);
        } catch (RejectedExecutionException ex) {
            String rejectedReason = closed ? "WORKER_STOPPED" : "WORKER_BUSY";
            batch.finish(local(rejectedReason));
            return batch.members.stream()
                    .map(w -> new Submission(false, rejectedReason, w.completion))
                    .toList();
        }
        return batch.members.stream().map(w -> new Submission(true, null, w.completion)).toList();
    }

    private Submission replay(Work work, Request request) {
        if (!work.request.sameInput(request)) return rejected("CHECK_ID_CONFLICT");
        return new Submission(true, null, work.completion);
    }

    private static Submission rejected(String reason) {
        return new Submission(false, reason, CompletableFuture.completedFuture(local(reason)));
    }

    private static CameraProbeResult local(String reason) {
        return new CameraProbeResult(Outcome.NOT_EXECUTED, reason, 0, 0, null);
    }

    private static CameraProbeResult cancelled() {
        return new CameraProbeResult(Outcome.CANCELLED, "CANCELLED", 0, 0, null);
    }

    private boolean reserveStart() {
        if (startIntervalNanos == 0) return true;
        while (true) {
            long now = System.nanoTime();
            long previous = nextStart.get();
            if (now - previous < 0) return false;
            if (nextStart.compareAndSet(previous, now + startIntervalNanos)) return true;
        }
    }

    public boolean awaitStopped(Duration timeout) throws InterruptedException {
        Objects.requireNonNull(timeout);
        if (timeout.isNegative() || timeout.compareTo(Duration.ofMinutes(1)) > 0)
            throw new IllegalArgumentException("Invalid stop timeout");
        return executor.awaitTermination(timeout.toNanos(), TimeUnit.NANOSECONDS);
    }

    @Override
    public void close() {
        List<Runnable> queued;
        synchronized (this) {
            closed = true;
            submitted.values().forEach(Work::requestCancellation);
            queued = executor.shutdownNow();
        }
        for (Runnable work : queued) {
            if (work instanceof Work single) single.finish(cancelled());
            else ((BatchWork) work).finish(cancelled());
        }
    }

    private final class BatchWork implements Runnable {
        private final List<Work> members;
        private boolean finished;

        private BatchWork(List<Request> requests) {
            members = requests.stream().map(r -> new Work(r, true)).toList();
        }

        @Override
        public void run() {
            var previous = MDC.getCopyOfContextMap();
            var owner = members.get(0);
            var active = new java.util.ArrayList<Work>();
            try {
                MDC.put("traceId", owner.request.traceId());
                if (sources.putIfAbsent(owner.request.sourceId(), owner) != null) {
                    finish(local("SOURCE_BUSY"));
                    return;
                }
                if (!reserveStart()) {
                    finish(local("START_BUDGET_BUSY"));
                    return;
                }
                for (var member : members) {
                    try {
                        if (member.cancellation.isCancelled()) {
                            member.finish(cancelled());
                            continue;
                        }
                        if (System.nanoTime() >= member.deadline) {
                            member.finish(local("QUEUE_DEADLINE_EXCEEDED"));
                            continue;
                        }
                        if (profiles.putIfAbsent(member.request.streamProfileId(), member)
                                != null) {
                            member.finish(local("PROFILE_BUSY"));
                            continue;
                        }
                        MDC.put("checkId", Long.toString(member.request.checkId()));
                        member.request.beforeProbe().run();
                        active.add(member);
                    } catch (CameraAdapterException ex) {
                        member.finish(local(RtspDescribeProbe.authorizationReason(ex)));
                    } catch (RuntimeException ex) {
                        member.finish(local("PROBE_UNAVAILABLE"));
                    }
                }
                if (!active.isEmpty()) {
                    if (platformProbe == null)
                        throw new CameraAdapterException("PLATFORM_STATUS_UNAVAILABLE");
                    var results =
                            platformProbe.executeBatch(
                                    active.stream()
                                            .map(
                                                    w ->
                                                            new CameraPlatformProbe.Target(
                                                                    w.request.mediaProfile(),
                                                                    w.request.authorization(),
                                                                    w.deadline,
                                                                    w.cancellation))
                                            .toList());
                    for (int i = 0; i < active.size(); i++) active.get(i).finish(results.get(i));
                }
            } catch (CameraAdapterException ex) {
                members.forEach(w -> w.finish(local(ex.reasonCode())));
            } catch (RuntimeException ex) {
                members.forEach(w -> w.finish(local("PLATFORM_STATUS_UNAVAILABLE")));
            } finally {
                finish(local("PROBE_UNAVAILABLE"));
                if (previous == null) MDC.clear();
                else MDC.setContextMap(previous);
            }
        }

        private synchronized void finish(CameraProbeResult fallback) {
            if (finished) return;
            finished = true;
            members.forEach(w -> w.finish(fallback));
            sources.remove(members.get(0).request.sourceId(), members.get(0));
            members.forEach(w -> profiles.remove(w.request.streamProfileId(), w));
            capacity.release();
        }
    }

    private final class Work implements Runnable {
        private final Request request;
        private final long deadline;
        private final RtspDescribeProbe.Cancellation cancellation =
                new RtspDescribeProbe.Cancellation();
        private final CompletableFuture<CameraProbeResult> completion = new CompletableFuture<>();
        private boolean finished;
        private final boolean grouped;

        private Work(Request request) {
            this(request, false);
        }

        private Work(Request request, boolean grouped) {
            this.request = request;
            this.grouped = grouped;
            deadline = System.nanoTime() + request.timeout().toNanos();
        }

        @Override
        public void run() {
            Map<String, String> previousMdc = MDC.getCopyOfContextMap();
            CameraProbeResult result = local("PROBE_UNAVAILABLE");
            try {
                MDC.put("traceId", request.traceId());
                MDC.put("checkId", Long.toString(request.checkId()));
                result = execute();
            } catch (CameraAdapterException ex) {
                result =
                        cancellation.isCancelled()
                                ? cancelled()
                                : local(RtspDescribeProbe.authorizationReason(ex));
            } catch (RuntimeException ex) {
                result = cancellation.isCancelled() ? cancelled() : local("PROBE_UNAVAILABLE");
                LOG.warn("Camera status probe unavailable checkId={}", request.checkId());
            } finally {
                finish(result);
                if (previousMdc == null) MDC.clear();
                else MDC.setContextMap(previousMdc);
            }
        }

        private CameraProbeResult execute() {
            if (cancellation.isCancelled()) return cancelled();
            if (System.nanoTime() - deadline >= 0) return local("QUEUE_DEADLINE_EXCEEDED");
            if (profiles.putIfAbsent(request.streamProfileId(), this) != null)
                return local("PROFILE_BUSY");
            if (sources.putIfAbsent(request.sourceId(), this) != null) return local("SOURCE_BUSY");
            if (!reserveStart()) return local("START_BUDGET_BUSY");
            if (cancellation.isCancelled()) return cancelled();
            request.beforeProbe().run();
            if (cancellation.isCancelled()) return cancelled();
            if (System.nanoTime() - deadline >= 0) return local("QUEUE_DEADLINE_EXCEEDED");
            if (request.layer() == CameraStatusLayer.MEDIA)
                return mediaProbe.execute(
                        request.checkId(),
                        request.traceId(),
                        request.mediaProfile(),
                        request.authorization(),
                        deadline,
                        cancellation);
            if (request.layer() == CameraStatusLayer.UPSTREAM)
                return platformProbe == null
                        ? local("PLATFORM_STATUS_UNAVAILABLE")
                        : platformProbe.execute(
                                request.mediaProfile(),
                                request.authorization(),
                                deadline,
                                cancellation);
            var target = request.target();
            if (request.targetResolver() != null) {
                try {
                    target =
                            request.targetResolver().resolve(deadline, this::checkResolutionActive);
                    checkResolutionActive();
                } catch (CameraAdapterException ex) {
                    return local(resolutionReason(ex));
                }
            }
            return probe.execute(target, deadline, cancellation);
        }

        private void checkResolutionActive() {
            if (cancellation.isCancelled()) throw new CameraAdapterException("CANCELLED");
            if (System.nanoTime() - deadline >= 0)
                throw new CameraAdapterException("STREAM_URI_TIMEOUT");
            request.target().authorizationCheck().run();
        }

        private synchronized boolean requestCancellation() {
            if (finished) return false;
            cancellation.cancel();
            return true;
        }

        private void finish(CameraProbeResult result) {
            synchronized (this) {
                if (finished) return;
                finished = true;
                if (cancellation.isCancelled()) result = cancelled();
                cancellation.afterResourcesStopped(
                        () -> {
                            profiles.remove(request.streamProfileId(), this);
                            if (!grouped) sources.remove(request.sourceId(), this);
                        });
                submitted.remove(request.checkId(), this);
                if (!grouped) capacity.release();
            }
            LOG.info(
                    "Camera status probe ended traceId={} checkId={} reasonCode={} durationMs={}",
                    request.traceId(),
                    request.checkId(),
                    result.reasonCode(),
                    result.durationMs());
            completion.complete(result);
        }
    }

    private static String resolutionReason(CameraAdapterException ex) {
        return switch (ex.reasonCode() == null ? "" : ex.reasonCode()) {
            case "STREAM_URI_TIMEOUT",
                    "STREAM_URI_AUTH_REJECTED",
                    "STREAM_URI_UNAVAILABLE",
                    "STREAM_URI_INVALID",
                    "NETWORK_BLOCKED",
                    "CANCELLED" ->
                    ex.reasonCode();
            default -> RtspDescribeProbe.authorizationReason(ex);
        };
    }
}
