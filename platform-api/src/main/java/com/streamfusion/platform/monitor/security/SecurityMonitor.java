package com.streamfusion.platform.monitor.security;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.monitor.diagnostics.DiagnosticLog;
import com.streamfusion.platform.system.auth.guard.ClientIpResolver;
import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import jakarta.servlet.http.HttpServletRequest;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.stereotype.Service;

/** Application signals, not payload inspection or an automatic blocking decision. */
@Service
@EnableConfigurationProperties(SecurityMonitorProperties.class)
public class SecurityMonitor {
    public static final String ACTOR_ATTRIBUTE = SecurityMonitor.class.getName() + ".actor";
    private static final Logger LOG = LoggerFactory.getLogger(SecurityMonitor.class);
    private static final int MAX_SOURCES = 2048;
    private static final int MAX_EVENTS = 128;
    private static final int MAX_ALERTS = 1000;
    private static final Duration RETENTION = Duration.ofDays(7);
    private static final Set<String> PASSIVE =
            Set.of("/auth/me", "/auth/session", "/auth/csrf", "/actuator/health", "/favicon.ico");
    private final SecurityMonitorProperties properties;
    private final Clock clock;
    private final ClientIpResolver ips;
    private final SecurityAlertStore store;
    private final ArrayBlockingQueue<Observation> queue = new ArrayBlockingQueue<>(1024);
    private final AtomicLong dropped = new AtomicLong();
    private final AtomicBoolean closed = new AtomicBoolean();
    private final LinkedHashMap<String, Bucket> sources = new LinkedHashMap<>(16, .75f, true);
    private final LinkedHashMap<String, SecurityAlert> alerts = new LinkedHashMap<>();
    private final String instanceId = UUID.randomUUID().toString();
    private ScheduledExecutorService worker;
    private long revision;
    private long evictedSources;
    private long truncatedWindows;
    private long processingFailures;
    private boolean dirty;
    private Instant lastFlush = Instant.EPOCH;

    public SecurityMonitor(
            SecurityMonitorProperties properties,
            PlatformProperties platform,
            Clock clock,
            ClientIpResolver ips,
            ObjectMapper json) {
        this.properties = properties;
        this.clock = clock;
        this.ips = ips;
        this.store = new SecurityAlertStore(platform.workDir(), json);
    }

    @PostConstruct
    void start() {
        if (!properties.enabled()) return;
        restore();
        worker =
                Executors.newSingleThreadScheduledExecutor(
                        r -> {
                            Thread thread = new Thread(r, "security-monitor");
                            thread.setDaemon(true);
                            return thread;
                        });
        worker.scheduleWithFixedDelay(this::tick, 100, 100, TimeUnit.MILLISECONDS);
    }

    synchronized void restore() {
        for (SecurityAlert alert : store.open()) alerts.put(alert.id(), alert);
        prune(clock.instant());
    }

    /** Snapshot safe scalars only; request threads never wait for a worker or write to disk. */
    public void observe(HttpServletRequest request, int status, boolean knownRoute) {
        if (!properties.enabled() || closed.get() || status < 400) return;
        try {
            String path = request.getRequestURI().substring(request.getContextPath().length());
            if (PASSIVE.contains(path)) return;
            Object code = request.getAttribute(DiagnosticLog.ERROR_CODE);
            SecurityAlert.Category category;
            if ("/auth/login/secure".equals(path)
                    && ("LOGIN_FAILED".equals(code) || "CAPTCHA_INVALID".equals(code))) {
                category = SecurityAlert.Category.LOGIN_FAILURE;
            } else if ("CSRF_INVALID".equals(code)) {
                category = SecurityAlert.Category.CSRF_FAILURE;
            } else if (!knownRoute
                    && !path.startsWith("/auth/")
                    && (status == 401 || status == 403 || status == 404 || status == 405)) {
                category = SecurityAlert.Category.PATH_PROBE;
            } else if ("FORBIDDEN".equals(code) || "UNAUTHORIZED".equals(code)) {
                category = SecurityAlert.Category.ACCESS_DENIED;
            } else return;
            // Hash a bounded path prefix only for diversity. It never appears in an alert/log.
            String digest =
                    HexFormat.of()
                            .formatHex(
                                    MessageDigest.getInstance("SHA-256")
                                            .digest(
                                                    path.substring(0, Math.min(512, path.length()))
                                                            .getBytes(StandardCharsets.UTF_8)));
            Object actor = request.getAttribute(ACTOR_ATTRIBUTE);
            Object trace = request.getAttribute("traceId");
            String traceId =
                    trace instanceof String value && value.matches("[a-f0-9]{32}") ? value : null;
            String actorId = actor instanceof Long value && value > 0 ? value.toString() : null;
            if (!queue.offer(
                    new Observation(
                            clock.instant(),
                            ips.resolve(request),
                            category,
                            digest,
                            actorId,
                            traceId))) dropped.incrementAndGet();
        } catch (Exception failure) {
            // Malformed proxy metadata and collection failures must not change the HTTP result.
            dropped.incrementAndGet();
        }
    }

    void tick() {
        try {
            for (int i = 0; i < 256; i++) {
                Observation observation = queue.poll();
                if (observation == null) break;
                synchronized (this) {
                    accept(observation, clock.instant());
                }
            }
            synchronized (this) {
                prune(clock.instant());
            }
            if (clock.instant().isBefore(lastFlush.plusSeconds(5))) return;
            flush();
        } catch (RuntimeException failure) {
            synchronized (this) {
                processingFailures++;
            }
            // No request data or unbounded per-event stack traces in the application log.
        }
    }

    private void accept(Observation observation, Instant now) {
        Instant cutoff = now.minus(properties.window());
        if (!observation.at().isAfter(cutoff)) {
            dropped.incrementAndGet();
            return;
        }
        Bucket bucket = sources.get(observation.ip());
        if (bucket == null) {
            if (sources.size() == MAX_SOURCES) {
                sources.remove(sources.keySet().iterator().next());
                evictedSources++;
            }
            bucket = new Bucket();
            sources.put(observation.ip(), bucket);
        }
        bucket.events.removeIf(e -> !e.at().isAfter(cutoff));
        if (bucket.events.size() == MAX_EVENTS) {
            bucket.events.removeFirst();
            truncatedWindows++;
        }
        bucket.events.addLast(observation);
        var counts = new EnumMap<SecurityAlert.Category, Integer>(SecurityAlert.Category.class);
        for (Observation event : bucket.events) counts.merge(event.category(), 1, Integer::sum);
        int paths =
                (int)
                        bucket.events.stream()
                                .filter(e -> e.category() == SecurityAlert.Category.PATH_PROBE)
                                .map(Observation::path)
                                .distinct()
                                .count();
        for (SecurityAlert.Rule rule : SecurityAlert.Rule.values()) {
            boolean match =
                    switch (rule) {
                        case LOGIN_FAILURE_BURST ->
                                counts.getOrDefault(SecurityAlert.Category.LOGIN_FAILURE, 0)
                                        >= properties.loginThreshold();
                        case ACCESS_DENIAL_BURST ->
                                counts.getOrDefault(SecurityAlert.Category.ACCESS_DENIED, 0)
                                        >= properties.deniedThreshold();
                        case PATH_SCAN ->
                                counts.getOrDefault(SecurityAlert.Category.PATH_PROBE, 0)
                                                >= properties.scanThreshold()
                                        && paths >= Math.min(8, properties.scanThreshold());
                        case CSRF_FAILURE_BURST ->
                                counts.getOrDefault(SecurityAlert.Category.CSRF_FAILURE, 0)
                                        >= properties.csrfThreshold();
                        case MIXED_PROBING ->
                                counts.size() >= 3
                                        && bucket.events.size() >= properties.mixedThreshold();
                    };
            if (!match) continue;
            Trigger previous = bucket.triggers.get(rule);
            boolean fresh =
                    previous == null || !now.isBefore(previous.at().plus(properties.cooldown()));
            String id = fresh ? UUID.randomUUID().toString() : previous.id();
            // An evicted retained alert is not resurrected repeatedly during its cooldown.
            if (!fresh && !alerts.containsKey(id)) continue;
            List<Observation> evidence = bucket.events.stream().toList();
            Instant first =
                    evidence.stream().map(Observation::at).min(Instant::compareTo).orElseThrow();
            Instant last =
                    evidence.stream().map(Observation::at).max(Instant::compareTo).orElseThrow();
            SecurityAlert alert =
                    new SecurityAlert(
                            id,
                            rule,
                            observation.ip(),
                            first,
                            last,
                            Math.toIntExact(properties.window().toSeconds()),
                            evidence.size(),
                            (int) evidence.stream().map(Observation::path).distinct().count(),
                            counts,
                            evidence.stream()
                                    .map(Observation::actorId)
                                    .filter(Objects::nonNull)
                                    .distinct()
                                    .limit(8)
                                    .toList(),
                            evidence.reversed().stream()
                                    .map(Observation::traceId)
                                    .filter(Objects::nonNull)
                                    .distinct()
                                    .limit(5)
                                    .toList());
            alerts.put(id, alert);
            while (alerts.size() > MAX_ALERTS) alerts.remove(alerts.keySet().iterator().next());
            dirty = true;
            if (fresh) {
                bucket.triggers.put(rule, new Trigger(id, now));
                revision++;
                LOG.warn(
                        "Security alert id={} rule={} sourceIp={} events={} traceId={}",
                        id,
                        rule,
                        alert.sourceIp(),
                        alert.eventCount(),
                        observation.traceId());
            }
        }
    }

    private void prune(Instant now) {
        sources.values()
                .removeIf(
                        b ->
                                b.events.isEmpty()
                                        || !b.events
                                                .getLast()
                                                .at()
                                                .plus(properties.cooldown())
                                                .isAfter(now));
        if (alerts.values().removeIf(a -> !a.lastSeen().plus(RETENTION).isAfter(now))) dirty = true;
        while (alerts.size() > MAX_ALERTS) {
            alerts.remove(alerts.keySet().iterator().next());
            dirty = true;
        }
    }

    private void flush() {
        List<SecurityAlert> snapshot;
        synchronized (this) {
            if (!dirty) return;
            snapshot = List.copyOf(alerts.values());
        }
        boolean saved = store.save(snapshot);
        synchronized (this) {
            if (saved) dirty = false;
        }
        lastFlush = clock.instant();
    }

    public synchronized Feed feed() {
        return new Feed(
                properties.enabled(),
                instanceId,
                revision,
                alerts.size(),
                Math.toIntExact(properties.window().toSeconds()),
                Math.toIntExact(properties.cooldown().toSeconds()),
                dropped.get(),
                evictedSources,
                truncatedWindows,
                processingFailures,
                store.healthy());
    }

    public synchronized Page page(int page, int size) {
        var retained =
                alerts.values().stream()
                        .filter(a -> a.lastSeen().plus(RETENTION).isAfter(clock.instant()))
                        .sorted(
                                Comparator.comparing(SecurityAlert::lastSeen)
                                        .reversed()
                                        .thenComparing(SecurityAlert::id))
                        .toList();
        return new Page(
                retained.stream().skip((long) (page - 1) * size).limit(size).toList(),
                page,
                size,
                retained.size(),
                feed());
    }

    public record Feed(
            boolean enabled,
            String instanceId,
            long revision,
            int retainedAlerts,
            int windowSeconds,
            int cooldownSeconds,
            long droppedEvents,
            long evictedSources,
            long truncatedWindows,
            long processingFailures,
            boolean persistenceHealthy) {}

    public record Page(List<SecurityAlert> items, int page, int size, int total, Feed monitor) {}

    private record Observation(
            Instant at,
            String ip,
            SecurityAlert.Category category,
            String path,
            String actorId,
            String traceId) {}

    private record Trigger(String id, Instant at) {}

    private static final class Bucket {
        final Deque<Observation> events = new ArrayDeque<>();
        final EnumMap<SecurityAlert.Rule, Trigger> triggers =
                new EnumMap<>(SecurityAlert.Rule.class);
    }

    @PreDestroy
    void close() {
        if (!closed.compareAndSet(false, true)) return;
        if (worker != null) {
            worker.shutdown();
            try {
                if (!worker.awaitTermination(2, TimeUnit.SECONDS)) {
                    worker.shutdownNow();
                    return;
                }
            } catch (InterruptedException failure) {
                Thread.currentThread().interrupt();
                return;
            }
        }
        if (properties.enabled()) {
            for (int i = 0; i < 4; i++) tick();
            flush();
        }
        store.close();
    }
}
