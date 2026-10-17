package com.streamfusion.platform.media.preview.node;

import jakarta.annotation.PreDestroy;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.boot.web.context.WebServerApplicationContext;
import org.springframework.context.event.EventListener;
import org.springframework.stereotype.Component;

/** Bounded control observations only; it never starts a media source as a health probe. */
@Component
public final class PreviewNodeObserver {
    private final PreviewNodeManager manager;
    private ScheduledExecutorService timer;

    public PreviewNodeObserver(PreviewNodeManager manager) {
        this.manager = manager;
    }

    @EventListener
    public synchronized void ready(ApplicationReadyEvent event) {
        if (!(event.getApplicationContext() instanceof WebServerApplicationContext)
                || timer != null) return;
        timer =
                Executors.newSingleThreadScheduledExecutor(
                        r -> {
                            var thread = new Thread(r, "preview-node-observer");
                            thread.setDaemon(true);
                            return thread;
                        });
        timer.scheduleWithFixedDelay(
                () -> {
                    try {
                        manager.check();
                    } catch (RuntimeException ignored) {
                    }
                },
                0,
                5,
                TimeUnit.SECONDS);
    }

    @PreDestroy
    public synchronized void close() {
        if (timer != null) timer.shutdownNow();
    }
}
