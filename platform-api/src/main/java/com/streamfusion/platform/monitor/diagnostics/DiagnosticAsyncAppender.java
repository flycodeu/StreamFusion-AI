package com.streamfusion.platform.monitor.diagnostics;

import ch.qos.logback.classic.AsyncAppender;
import ch.qos.logback.classic.spi.ILoggingEvent;
import java.util.concurrent.atomic.AtomicLong;

/** Only best-effort diagnostic events use this queue; persisted audit records are unaffected. */
public class DiagnosticAsyncAppender extends AsyncAppender {
    private static final AtomicLong DROPPED = new AtomicLong();
    private final Object producers = new Object();

    public DiagnosticAsyncAppender() {
        setNeverBlock(true);
        setDiscardingThreshold(0);
    }

    @Override
    protected void append(ILoggingEvent event) {
        // Serialize only producer offers, never disk IO. The consumer can only free capacity.
        synchronized (producers) {
            if (getRemainingCapacity() == 0) DROPPED.incrementAndGet();
            else super.append(event);
        }
    }

    public static long droppedEvents() {
        return DROPPED.get();
    }
}
