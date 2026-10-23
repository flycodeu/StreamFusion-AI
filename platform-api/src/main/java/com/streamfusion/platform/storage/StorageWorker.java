package com.streamfusion.platform.storage;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.storage.entity.StorageTask;
import com.streamfusion.platform.storage.mapper.StorageInstanceMapper;
import com.streamfusion.platform.storage.s3.StorageRemoteException;
import jakarta.annotation.PreDestroy;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import lombok.RequiredArgsConstructor;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.context.event.EventListener;
import org.springframework.core.env.Environment;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

/** Two bounded workers; periodic checks never write probe objects or delete stored files. */
@Component
@RequiredArgsConstructor
public class StorageWorker {
    private final StorageTaskService tasks;
    private final StorageInspectionService inspection;
    private final StorageCleanupService cleanup;
    private final StorageFolderService folders;
    private final StorageInstanceMapper instances;
    private final StorageJson json;
    private final StorageHistoryService history;
    private static final org.slf4j.Logger LOG =
            org.slf4j.LoggerFactory.getLogger(StorageWorker.class);
    private final Environment environment;
    private final String owner = UUID.randomUUID().toString();
    private final ThreadPoolExecutor workers =
            new ThreadPoolExecutor(
                    2,
                    2,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(2),
                    runnable -> {
                        var t = new Thread(runnable, "storage-worker");
                        t.setDaemon(true);
                        return t;
                    },
                    new ThreadPoolExecutor.AbortPolicy());
    private final Semaphore slots = new Semaphore(2);

    private boolean test() {
        return Arrays.asList(environment.getActiveProfiles()).contains("test");
    }

    @EventListener(ApplicationReadyEvent.class)
    public void recover() {
        if (!test()) tasks.recover();
    }

    @Scheduled(fixedDelay = 1000)
    public void tick() {
        if (test() || !slots.tryAcquire()) return;
        StorageTask task = null;
        try {
            task = tasks.claim(owner);
            if (task == null) {
                slots.release();
                return;
            }
            StorageTask selected = task;
            workers.execute(
                    () -> {
                        try {
                            execute(selected);
                        } finally {
                            slots.release();
                        }
                    });
        } catch (RuntimeException ex) {
            slots.release();
            if (task != null)
                tasks.finish(task, "FAILED", Map.of("reasonCode", "STORAGE_WORKER_UNAVAILABLE"));
        }
    }

    public void execute(StorageTask task) {
        try (var trace = org.slf4j.MDC.putCloseable("traceId", task.getTraceId());
                var run = org.slf4j.MDC.putCloseable("runId", task.getRunId().toString());
                var taskId = org.slf4j.MDC.putCloseable("taskId", task.getId().toString())) {
            LOG.info(
                    "Storage task started taskId={} runId={} type={}",
                    task.getId(),
                    task.getRunId(),
                    task.getType());
            tasks.validate(task);
            switch (task.getType()) {
                case "CHECK" -> inspection.check(task);
                case "USAGE_SCAN" -> inspection.scan(task);
                case "CLEANUP_PREVIEW" -> cleanup.build(task);
                case "FOLDER_PREVIEW" -> folders.build(task);
                case "FOLDER_DELETE" -> folders.delete(task);
                default -> throw new StorageRemoteException("STORAGE_TASK_TYPE_INVALID", 0);
            }
            LOG.info(
                    "Storage task finished taskId={} runId={} status={}",
                    task.getId(),
                    task.getRunId(),
                    tasks.require(task.getId()).getStatus());
        } catch (RuntimeException ex) {
            if (task.getType().startsWith("FOLDER_")) folders.failed(task, ex);
            cleanup.failPreview(task);
            String reason =
                    ex instanceof StorageRemoteException remote
                            ? remote.reason()
                            : ex instanceof BusinessException business
                                    ? business.code().name()
                                    : "STORAGE_TASK_FAILED";
            LOG.warn(
                    "Storage task stopped traceId={} taskId={} runId={} reasonCode={} exceptionType={}",
                    task.getTraceId(),
                    task.getId(),
                    task.getRunId(),
                    reason,
                    ex.getClass().getSimpleName());
            tasks.finish(
                    task,
                    reason.equals("CANCELLED") ? "CANCELLED" : "FAILED",
                    Map.of(
                            "reasonCode",
                            reason,
                            "suggestion",
                            "核对接入配置与权限；用traceId、taskId和runId定位日志"));
        }
    }

    @Scheduled(fixedDelay = 60000, initialDelay = 60000)
    public void maintenance() {
        if (test()) return;
        tasks.recover();
        history.prune();
    }

    @Scheduled(fixedDelay = 300000, initialDelay = 15000)
    public void observations() {
        if (test()) return;
        for (var instance : tasks.dueInstances(instances)) {
            if (json.read(instance.getObservation()).path("checkedAt").asLong() + 300
                    > Instant.now().getEpochSecond()) continue;
            try {
                tasks.enqueue(
                        instance.getId(),
                        "CHECK",
                        instance.getVersion(),
                        UUID.randomUUID().toString(),
                        instance.getCreatedBy(),
                        Map.of("mode", "READ_ONLY", "scopeId", ""),
                        false);
            } catch (RuntimeException ignored) {
                /* Busy/revoked/unavailable remains visible in prior task/observation. */
            }
        }
    }

    @PreDestroy
    public void stop() {
        workers.shutdownNow();
    }
}
