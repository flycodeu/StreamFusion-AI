package com.streamfusion.platform.camera.access.service;

import com.streamfusion.platform.camera.access.adapter.*;
import jakarta.annotation.PreDestroy;
import java.util.concurrent.*;
import lombok.RequiredArgsConstructor;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.context.event.EventListener;
import org.springframework.stereotype.Component;

/** Four workers and sixteen queue slots; no networking inside asset transactions. */
@Component
@RequiredArgsConstructor
public class CameraAccessWorker {
    private static final Logger LOG = LoggerFactory.getLogger(CameraAccessWorker.class);
    private final CameraAccessJobsService jobs;
    private final CameraConnectionRules connections;
    private final CameraAccessAdapterRegistry adapters;
    private final CameraNetworkScan scans;
    private final CameraBulkImportService bulk;
    private final java.util.Set<Long> submitted = ConcurrentHashMap.newKeySet();
    private final ThreadPoolExecutor executor =
            new ThreadPoolExecutor(
                    4,
                    4,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(16),
                    r -> {
                        var t = new Thread(r, "camera-discovery");
                        t.setDaemon(true);
                        return t;
                    },
                    new ThreadPoolExecutor.AbortPolicy());
    private final ScheduledExecutorService cleanup =
            Executors.newSingleThreadScheduledExecutor(
                    r -> {
                        var thread = new Thread(r, "camera-discovery-cleanup");
                        thread.setDaemon(true);
                        return thread;
                    });

    @EventListener(ApplicationReadyEvent.class)
    public void start(ApplicationReadyEvent event) {
        // Bootstrap commands and mock servlet tests have no running HTTP server.
        if (!(event.getApplicationContext()
                instanceof org.springframework.boot.web.context.WebServerApplicationContext))
            return;
        jobs.maintenance(true);
        for (int batch = 0; batch < 3; batch++) jobs.maintenance(false);
        cleanup.scheduleWithFixedDelay(
                () -> {
                    try {
                        jobs.maintenance(false);
                    } catch (RuntimeException ex) {
                        LOG.warn("Camera discovery cleanup unavailable");
                    }
                },
                60,
                60,
                TimeUnit.SECONDS);
    }

    public void submit(long id) {
        if (!submitted.add(id)) return;
        try {
            executor.execute(() -> discover(id));
        } catch (RejectedExecutionException ex) {
            submitted.remove(id);
            jobs.failQueued(id, "DISCOVERY_BUSY");
        }
    }

    private void discover(long id) {
        CameraAccessJobsService.Work work = null;
        try {
            work = jobs.claim(id);
            if (work == null) return;
            if (work.traceId() != null) MDC.put("traceId", work.traceId());
            var connection = work.payload().connection();
            var claimed = work;
            jobs.checkWork(work);
            if (work.payload().bulk() != null) {
                importPlatform(work);
                return;
            }
            if (work.payload().scan() != null) {
                jobs.completeScan(
                        work, scans.execute(work.payload().scan(), () -> jobs.checkWork(claimed)));
                return;
            }
            var catalog =
                    "RTSP".equals(connection.method())
                            ? connections.rtspCatalog(connection)
                            : adapters.discover(
                                    connection.method(),
                                    new CameraAccessContext(
                                            connections.endpoint(connection),
                                            connection.networkPolicyKey(),
                                            connection.username(),
                                            connection.password(),
                                            connection.rtspPort(),
                                            () -> jobs.checkWork(claimed),
                                            System.nanoTime() + TimeUnit.SECONDS.toNanos(60),
                                            connection.pageNumber() == null
                                                    ? 1
                                                    : connection.pageNumber(),
                                            connection.pageSize() == null
                                                    ? 100
                                                    : connection.pageSize()));
            jobs.complete(work, catalog);
        } catch (CameraAdapterException ex) {
            if (work == null) jobs.failQueued(id, ex.reasonCode());
            else jobs.fail(id, work.version(), ex.reasonCode());
            LOG.info("Camera discovery ended jobId={} reasonCode={}", id, ex.reasonCode());
        } catch (RuntimeException ex) {
            if (work == null) jobs.failQueued(id, "DISCOVERY_UNAVAILABLE");
            else jobs.fail(id, work.version(), "DISCOVERY_UNAVAILABLE");
            LOG.warn("Camera discovery unavailable jobId={}", id);
        } finally {
            submitted.remove(id);
            MDC.remove("traceId");
            // A start request can requeue this ticket after complete committed but before this
            // worker exits. The shared set still deduplicates a concurrent HTTP resubmission.
            // Claim failure and queue rejection terminate QUEUED, so this is not an unbounded
            // retry.
            if (jobs.queued(id)) submit(id);
        }
    }

    /** Only one page is resident; each resource commits independently through the service proxy. */
    private void importPlatform(CameraAccessJobsService.Work initial) {
        var current = new java.util.concurrent.atomic.AtomicReference<>(initial);
        try {
            var connection = initial.payload().connection();
            for (int pageNumber = 1;
                    pageNumber <= CameraBulkImportService.MAX_PAGES;
                    pageNumber++) {
                bulk.checkWork(current.get());
                var page =
                        adapters.discover(
                                connection.method(),
                                new CameraAccessContext(
                                        connections.endpoint(connection),
                                        connection.networkPolicyKey(),
                                        connection.username(),
                                        connection.password(),
                                        connection.rtspPort(),
                                        () -> bulk.checkWork(current.get()),
                                        System.nanoTime() + TimeUnit.SECONDS.toNanos(60),
                                        pageNumber,
                                        CameraBulkImportService.PAGE_SIZE));
                current.set(bulk.beginPage(current.get(), page));
                for (int index = 0; index < page.channels().size(); index++) {
                    try {
                        current.set(bulk.importItem(current.get(), page, index));
                    } catch (com.streamfusion.platform.common.exception.BusinessException ex) {
                        if (!java.util.Set.of(
                                        com.streamfusion.platform.common.exception.ErrorCode
                                                .VALIDATION_ERROR,
                                        com.streamfusion.platform.common.exception.ErrorCode
                                                .CONFLICT)
                                .contains(ex.code())) throw ex;
                        current.set(
                                bulk.failedItem(current.get(), page, index, "IMPORT_ITEM_INVALID"));
                    }
                }
                var next = bulk.finishPage(current.get(), page);
                if (next == null) return;
                current.set(next);
            }
        } catch (CameraAdapterException ex) {
            jobs.fail(initial.id(), current.get().version(), ex.reasonCode());
            LOG.info(
                    "Camera bulk import ended jobId={} reasonCode={}",
                    initial.id(),
                    ex.reasonCode());
        } catch (RuntimeException ex) {
            jobs.fail(initial.id(), current.get().version(), "IMPORT_UNAVAILABLE");
            LOG.warn("Camera bulk import unavailable jobId={}", initial.id());
        }
    }

    @PreDestroy
    public void stop() {
        executor.shutdownNow();
        cleanup.shutdownNow();
    }
}
