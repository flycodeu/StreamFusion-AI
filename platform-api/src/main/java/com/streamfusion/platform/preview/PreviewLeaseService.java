package com.streamfusion.platform.preview;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import com.streamfusion.platform.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.preview.media.PreviewMediaGateway.Transcode;
import com.streamfusion.platform.preview.source.PreviewLocatorService;
import jakarta.annotation.PreDestroy;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.stereotype.Service;

/** Bounded ephemeral leases; each lease owns an independently revocable ZLM tuple. */
@Service
public class PreviewLeaseService {
    private static final Logger LOG = LoggerFactory.getLogger(PreviewLeaseService.class);
    private static final Duration TTL = Duration.ofSeconds(90);
    private static final int MAX_LEASES = 256;
    private static final int MAX_USER_LEASES = 64;
    private static final int MAX_CLEANUP_ATTEMPTS = 6;
    private final PreviewAuthorization authorization;
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final PreviewLocatorService locators;
    private final PreviewMediaGateway media;
    private final MediaRuntimeService runtimes;
    private final Clock clock;
    private final String app =
            "sf_preview_" + UUID.randomUUID().toString().replace("-", "").substring(0, 12);
    private final Object guard = new Object();
    private final Map<String, Lease> leases = new HashMap<>();
    private final Map<RequestKey, RequestEntry> requests = new HashMap<>();
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
            CameraChannelMapper channels,
            CameraProfileMapper profiles,
            PreviewLocatorService locators,
            PreviewMediaGateway media,
            MediaRuntimeService runtimes,
            Clock clock) {
        this.authorization = authorization;
        this.channels = channels;
        this.profiles = profiles;
        this.locators = locators;
        this.media = media;
        this.runtimes = runtimes;
        this.clock = clock;
        reaper.scheduleWithFixedDelay(this::reapSafely, 10, 10, TimeUnit.SECONDS);
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
            requests.entrySet()
                    .removeIf(
                            e ->
                                    e.getValue().expiresAt.isBefore(now())
                                            && e.getValue().result.isDone());
            entry = requests.get(key);
            existing = entry != null;
            if (existing) {
                if (!entry.input.equals(input)) throw BusinessException.error(ErrorCode.CONFLICT);
                lease = null;
            } else {
                if (leases.size() >= MAX_LEASES
                        || requests.size() >= MAX_LEASES * 2
                        || leases.values().stream()
                                        .filter(v -> v.owner.getUserId() == owner.getUserId())
                                        .count()
                                >= MAX_USER_LEASES)
                    throw BusinessException.error(ErrorCode.RATE_LIMITED);
                String id = UUID.randomUUID().toString();
                lease = new Lease(id, owner, cameraId, input, now().plus(TTL), MDC.get("traceId"));
                leases.put(id, lease);
                entry = new RequestEntry(input, lease.expiresAt);
                requests.put(key, entry);
            }
        }
        if (existing) {
            try {
                var result = entry.result.get(30, TimeUnit.SECONDS);
                if (result.leaseId() != null) {
                    var active = require(result.leaseId());
                    authorization.request(active.owner, active.cameraId);
                    return view(active);
                }
                return result;
            } catch (InterruptedException ex) {
                Thread.currentThread().interrupt();
                throw new PreviewFailure("CANCELLED");
            } catch (java.util.concurrent.ExecutionException
                    | java.util.concurrent.TimeoutException ex) {
                throw new PreviewFailure("PREVIEW_PENDING");
            }
        }
        try {
            var result = initialize(lease);
            entry.result.complete(result);
            return result;
        } catch (BusinessException ex) {
            release(lease);
            entry.result.completeExceptionally(ex);
            throw ex;
        } catch (RuntimeException ex) {
            String reason =
                    ex instanceof PreviewFailure failure ? failure.reason() : "PREVIEW_UNAVAILABLE";
            if (ex instanceof PreviewFailure failure && failure.pendingTranscode() != null)
                lease.transcode = failure.pendingTranscode();
            release(lease);
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

    private PreviewModels.LeaseView initialize(Lease lease) {
        var channel = channels.selectById(lease.cameraId);
        if (channel == null) throw new PreviewFailure("CAMERA_NOT_FOUND");
        if (!"ENABLED".equals(channel.getLifecycle())) throw new PreviewFailure("CAMERA_DISABLED");
        var profile =
                chooseProfile(
                        lease.cameraId,
                        lease.input.streamProfileId(),
                        lease.input.usage(),
                        channel.getDefaultPreviewProfileId());
        if (profile == null) throw new PreviewFailure("PROFILE_NOT_FOUND");
        lease.profileId = profile.getId();
        if (!Boolean.TRUE.equals(profile.getEnabled()))
            throw new PreviewFailure("PROFILE_DISABLED");
        var runtime = runtimes.activeZlm().orElseThrow(() -> new PreviewFailure("ZLM_NOT_READY"));
        var source =
                locators.resolve(profile, () -> authorization.request(lease.owner, lease.cameraId));
        String id = lease.id.replace("-", "");
        synchronized (lease) {
            if (lease.closing) throw new PreviewFailure("LEASE_EXPIRED");
            // Retain the exact tuple before mutation, including timeout with unknown outcome.
            lease.stream = new Stream(runtime, app, id, "__defaultVhost__/" + app + "/" + id);
            lease.stream = media.open(runtime, app, id, source.uri());
            var stats = awaitTracks(lease.stream, id);
            String codec = normalize(stats.videoCodec());
            if (!lease.input.videoCodecs().contains(codec)) {
                if (!"H265".equals(codec) || !lease.input.videoCodecs().contains("H264"))
                    throw new PreviewFailure("CODEC_UNSUPPORTED");
                lease.outputId = id + "h264";
                lease.transcode = media.transcode(lease.stream, lease.outputId);
                try {
                    stats = awaitTracks(lease.stream, lease.outputId);
                } catch (PreviewFailure failure) {
                    throw new PreviewFailure(failure.reason(), lease.transcode);
                }
                if (!"H264".equals(normalize(stats.videoCodec())))
                    throw new PreviewFailure("TRANSCODE_CODEC_MISMATCH");
                lease.transcoded = true;
            }
            authorization.request(lease.owner, lease.cameraId);
            lease.codec = normalize(stats.videoCodec());
            lease.ready = true;
            lease.expiresAt = now().plus(TTL);
            return view(lease);
        }
    }

    private PreviewModels.Stats awaitTracks(Stream stream, String output) {
        for (int attempt = 0; attempt < 4; attempt++) {
            var stats = media.stats(stream, output);
            if ("READY".equals(stats.state()) && stats.videoCodec() != null) return stats;
            if (attempt < 3) {
                try {
                    TimeUnit.MILLISECONDS.sleep(200);
                } catch (InterruptedException ex) {
                    Thread.currentThread().interrupt();
                    throw new PreviewFailure("CANCELLED");
                }
            }
        }
        throw new PreviewFailure("MEDIA_NOT_READY");
    }

    public PreviewModels.LeaseView renew(String id) {
        var lease = owned(id);
        synchronized (lease) {
            checkReady(lease);
            requireEnabled(lease);
            lease.expiresAt = now().plus(TTL);
            return view(lease);
        }
    }

    public PreviewModels.Answer answer(String id, PreviewModels.Offer offer) {
        if (offer == null
                || offer.sdp() == null
                || offer.sdp().length() > 65536
                || !offer.sdp().startsWith("v=0"))
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        var lease = owned(id);
        synchronized (lease) {
            checkReady(lease);
            requireEnabled(lease);
            if (lease.signalled) throw new PreviewFailure("SIGNAL_ALREADY_CREATED");
            var answer = media.answer(lease.stream, output(lease), offer.sdp());
            lease.signalled = true;
            authorization.request(lease.owner, lease.cameraId);
            return answer;
        }
    }

    public PreviewModels.Stats stats(String id) {
        var lease = owned(id);
        synchronized (lease) {
            checkReady(lease);
            requireEnabled(lease);
            return media.stats(lease.stream, output(lease));
        }
    }

    public void delete(String id) {
        var lease = owned(id);
        release(lease);
        if (lease.closing) throw new PreviewFailure("CLEANUP_PENDING");
    }

    private Lease owned(String id) {
        var lease = require(id);
        try {
            authorization.request(lease.owner, lease.cameraId);
        } catch (RuntimeException ex) {
            // A different caller cannot terminate an owner's stream; revoked owners are reaped.
            if (!(ex instanceof PreviewFailure)) release(lease);
            throw ex;
        }
        return lease;
    }

    private Lease require(String id) {
        if (id == null || !id.matches("[0-9a-f-]{36}"))
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        Lease value;
        synchronized (guard) {
            value = leases.get(id);
        }
        if (value == null || !now().isBefore(value.expiresAt) || value.closing)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        return value;
    }

    private void checkReady(Lease lease) {
        if (!lease.ready || lease.closing || !now().isBefore(lease.expiresAt))
            throw new PreviewFailure("LEASE_EXPIRED");
    }

    private void requireEnabled(Lease lease) {
        var channel = channels.selectById(lease.cameraId);
        var profile = profiles.selectById(lease.profileId);
        if (channel == null
                || !"ENABLED".equals(channel.getLifecycle())
                || profile == null
                || !Boolean.TRUE.equals(profile.getEnabled())) {
            release(lease);
            throw new PreviewFailure("CAMERA_DISABLED");
        }
    }

    private CameraProfileEntity chooseProfile(
            long camera, String id, String requested, Long preferred) {
        var values =
                profiles.selectList(
                        new LambdaQueryWrapper<CameraProfileEntity>()
                                .eq(CameraProfileEntity::getChannelId, camera)
                                .orderByAsc(CameraProfileEntity::getId));
        if (id != null) {
            long profile = DecimalInput.id(id, "streamProfileId");
            return values.stream().filter(p -> p.getId().equals(profile)).findFirst().orElse(null);
        }
        var enabled = values.stream().filter(p -> Boolean.TRUE.equals(p.getEnabled())).toList();
        if (requested != null) {
            var match =
                    enabled.stream()
                            .filter(p -> requested.equals(PreviewCatalogService.usage(p)))
                            .findFirst();
            if (match.isPresent()) return match.get();
        }
        var defaults = enabled.stream().filter(p -> p.getId().equals(preferred)).findFirst();
        if (defaults.isPresent()) return defaults.get();
        return enabled.stream()
                .filter(p -> "MAIN".equals(PreviewCatalogService.usage(p)))
                .findFirst()
                .orElse(enabled.isEmpty() ? null : enabled.getFirst());
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
                || !Set.of("H264", "H265").containsAll(input.videoCodecs())
                || input.usage() != null && !Set.of("MAIN", "SUB").contains(input.usage()))
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }

    private static String normalize(String codec) {
        if (codec == null) return "";
        String v = codec.toUpperCase(Locale.ROOT).replace(".", "");
        return v.equals("AVC") ? "H264" : v.equals("HEVC") ? "H265" : v;
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

    private static String output(Lease lease) {
        return lease.transcoded ? lease.outputId : lease.stream.id();
    }

    private Instant now() {
        return clock.instant();
    }

    private void release(Lease lease) {
        synchronized (lease) {
            lease.closing = true;
            lease.ready = false;
            if (lease.cleanupAttempts >= MAX_CLEANUP_ATTEMPTS) return;
            lease.cleanupAttempts++;
            try {
                if (lease.stream != null) media.close(lease.stream, lease.transcode);
                lease.closing = false;
                synchronized (guard) {
                    leases.remove(lease.id, lease);
                }
            } catch (RuntimeException ex) {
                lease.nextCleanupAt = now().plusSeconds(Math.min(60, 5L << lease.cleanupAttempts));
                LOG.warn(
                        "Preview cleanup pending traceId={} leaseId={} attempt={} reasonCode={}",
                        lease.traceId,
                        lease.id,
                        lease.cleanupAttempts,
                        "ZLM_UNAVAILABLE");
            }
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
                lease.closing = true;
                if (asynchronous) queueCleanup(lease);
                else release(lease);
            }
        }
    }

    private void queueCleanup(Lease lease) {
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
            sweep(true);
        } catch (RuntimeException ex) {
            LOG.warn("Preview lease sweep deferred reasonCode={}", "DEPENDENCY_UNAVAILABLE");
        }
    }

    @PreDestroy
    public void close() {
        reaper.shutdownNow();
        List<Lease> values;
        synchronized (guard) {
            values = List.copyOf(leases.values());
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
        final CompletableFuture<PreviewModels.LeaseView> result = new CompletableFuture<>();

        RequestEntry(PreviewModels.CreateLease input, Instant expiresAt) {
            this.input = input;
            this.expiresAt = expiresAt;
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
        boolean ready;
        volatile boolean closing;
        boolean signalled;
        boolean transcoded;
        String codec;
        String outputId;
        Stream stream;
        Transcode transcode;
        int cleanupAttempts;
        Instant nextCleanupAt;
        final AtomicBoolean cleanupQueued = new AtomicBoolean();

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
