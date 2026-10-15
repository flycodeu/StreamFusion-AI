package com.streamfusion.platform.camera.status;

import jakarta.annotation.PreDestroy;
import java.util.UUID;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import lombok.RequiredArgsConstructor;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.ApplicationArguments;
import org.springframework.boot.ApplicationRunner;
import org.springframework.context.annotation.Profile;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;

/**
 * Recovery follows schema validation. Tests call recovery explicitly after creating their schema.
 */
@Component
@Profile("!test")
@Order(10)
@RequiredArgsConstructor
public class CameraStatusMaintenance implements ApplicationRunner {
    private static final Logger LOG = LoggerFactory.getLogger(CameraStatusMaintenance.class);
    private final CameraStatusService service;
    private final CameraStatusPlanService plans;
    private final org.springframework.context.ApplicationContext context;
    private long nextPatrolWarning;
    private final ScheduledExecutorService cleanup =
            Executors.newSingleThreadScheduledExecutor(
                    task -> {
                        var thread = new Thread(task, "camera-status-cleanup");
                        thread.setDaemon(true);
                        return thread;
                    });

    @Override
    public void run(ApplicationArguments args) {
        if (!(context instanceof org.springframework.boot.web.context.WebServerApplicationContext))
            return;
        service.recoverInterruptedOnStartup();
        cleanup.scheduleWithFixedDelay(
                () -> {
                    try {
                        plans.tick();
                    } catch (RuntimeException ex) {
                        if (System.nanoTime() >= nextPatrolWarning) {
                            nextPatrolWarning = System.nanoTime() + TimeUnit.MINUTES.toNanos(1);
                            LOG.warn(
                                    "Camera patrol unavailable traceId={} errorType={}",
                                    UUID.randomUUID().toString().replace("-", ""),
                                    ex.getClass().getSimpleName());
                        }
                    }
                },
                1,
                1,
                TimeUnit.SECONDS);
        cleanup.scheduleWithFixedDelay(
                () -> {
                    try {
                        service.maintenance();
                    } catch (RuntimeException ex) {
                        LOG.warn(
                                "Camera status maintenance unavailable traceId={}",
                                UUID.randomUUID().toString().replace("-", ""));
                    }
                },
                5,
                5,
                TimeUnit.SECONDS);
    }

    @PreDestroy
    public void close() {
        cleanup.shutdownNow();
    }
}
