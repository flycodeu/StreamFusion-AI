package com.streamfusion.platform.server.service;

import com.streamfusion.platform.server.config.ServerMonitorProperties;
import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import jakarta.annotation.PreDestroy;
import java.time.Clock;
import java.time.Instant;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import org.springframework.stereotype.Service;

/** One collector and a one-task handoff bound; concurrent reads share one response deadline. */
@Service
public class ServerMonitorService {
    private final ServerCollector collector;
    private final ServerMonitorProperties properties;
    private final Clock clock;
    private final ThreadPoolExecutor worker =
            new ThreadPoolExecutor(
                    1,
                    1,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(1),
                    Thread.ofPlatform().daemon().name("server-monitor", 0).factory());
    private final ScheduledExecutorService deadline =
            Executors.newSingleThreadScheduledExecutor(
                    Thread.ofPlatform().daemon().name("server-monitor-deadline", 0).factory());
    private ServerSnapshotVo cached;
    private CompletableFuture<ServerSnapshotVo> inFlight;
    private boolean collecting;
    private Thread collectorThread;
    private Instant nextCollectionAt = Instant.MIN;

    public ServerMonitorService(
            ServerCollector collector, ServerMonitorProperties properties, Clock clock) {
        this.collector = collector;
        this.properties = properties;
        this.clock = clock;
    }

    public synchronized CompletableFuture<ServerSnapshotVo> snapshot() {
        if (cached != null
                && clock.instant().isBefore(cached.sampledAt().plus(properties.cacheTtl())))
            return CompletableFuture.completedFuture(cached);
        if (collecting) return inFlight;
        if (clock.instant().isBefore(nextCollectionAt))
            return CompletableFuture.completedFuture(fallback());
        collecting = true;
        nextCollectionAt = clock.instant().plus(properties.cacheTtl());
        var result = new CompletableFuture<ServerSnapshotVo>();
        inFlight = result;
        var timer =
                deadline.schedule(
                        () -> expire(result),
                        properties.collectionTimeout().toMillis(),
                        TimeUnit.MILLISECONDS);
        try {
            worker.execute(
                    () -> {
                        try {
                            synchronized (this) {
                                collectorThread = Thread.currentThread();
                                if (result.isDone()) return;
                            }
                            ServerSnapshotVo sample = collector.collect();
                            synchronized (this) {
                                if (!result.isDone() && !Thread.currentThread().isInterrupted()) {
                                    cached = sample;
                                    result.complete(sample);
                                }
                            }
                        } catch (RuntimeException ignored) {
                            synchronized (this) {
                                result.complete(fallback());
                            }
                        } finally {
                            timer.cancel(false);
                            synchronized (this) {
                                collecting = false;
                                collectorThread = null;
                            }
                        }
                    });
        } catch (RuntimeException ignored) {
            collecting = false;
            timer.cancel(false);
            result.complete(fallback());
        }
        return result;
    }

    private synchronized void expire(CompletableFuture<ServerSnapshotVo> result) {
        if (inFlight == result && result.complete(fallback()) && collectorThread != null)
            collectorThread.interrupt();
    }

    private synchronized ServerSnapshotVo fallback() {
        return cached == null ? ServerSnapshotVo.unavailable() : cached.asStale("UNAVAILABLE");
    }

    @PreDestroy
    public void close() {
        worker.shutdownNow();
        deadline.shutdownNow();
    }
}
