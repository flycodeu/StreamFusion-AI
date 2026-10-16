package com.streamfusion.platform.monitor.audit.maintenance;

import jakarta.annotation.PreDestroy;
import java.util.UUID;
import java.util.concurrent.*;
import lombok.RequiredArgsConstructor;
import org.slf4j.*;
import org.springframework.boot.*;
import org.springframework.context.annotation.Profile;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;

/** Dedicated to logs; one periodic task, no per-request queue and no probe threads. */
@Component
@Profile("!test")
@Order(20)
@RequiredArgsConstructor
public class AuditMaintenanceWorker implements ApplicationRunner {
    private static final Logger LOG = LoggerFactory.getLogger(AuditMaintenanceWorker.class);
    private final AuditRetentionService retention;
    private final AuditExportService exports;
    private final org.springframework.context.ApplicationContext context;
    private final java.util.concurrent.atomic.AtomicBoolean wakePending =
            new java.util.concurrent.atomic.AtomicBoolean();
    private final ScheduledExecutorService worker =
            Executors.newSingleThreadScheduledExecutor(
                    r -> {
                        var t = new Thread(r, "audit-maintenance");
                        t.setDaemon(true);
                        return t;
                    });

    @Override
    public void run(ApplicationArguments args) {
        if (!(context instanceof org.springframework.boot.web.context.WebServerApplicationContext))
            return;
        worker.scheduleWithFixedDelay(this::tick, 2, 60, TimeUnit.SECONDS);
    }

    public void wake() {
        if (!wakePending.compareAndSet(false, true)) return;
        try {
            worker.execute(
                    () -> {
                        boolean more = false;
                        try {
                            more = tick();
                        } finally {
                            wakePending.set(false);
                        }
                        if (more) wake();
                    });
        } catch (RejectedExecutionException ex) {
            wakePending.set(false);
        }
    }

    private boolean tick() {
        String traceId = UUID.randomUUID().toString().replace("-", "");
        try (var trace = MDC.putCloseable("traceId", traceId)) {
            exports.expireFiles();
            boolean more = exports.workOne();
            retention.tick();
            return more;
        } catch (RuntimeException ex) {
            LOG.warn(
                    "Audit maintenance unavailable traceId={} errorType={}",
                    traceId,
                    ex.getClass().getSimpleName());
            return false;
        }
    }

    @PreDestroy
    public void close() {
        worker.shutdownNow();
    }
}
