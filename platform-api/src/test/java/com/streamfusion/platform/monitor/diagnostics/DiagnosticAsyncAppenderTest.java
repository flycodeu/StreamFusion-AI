package com.streamfusion.platform.monitor.diagnostics;

import static org.assertj.core.api.Assertions.*;

import ch.qos.logback.classic.Level;
import ch.qos.logback.classic.LoggerContext;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.classic.spi.LoggingEvent;
import ch.qos.logback.core.AppenderBase;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class DiagnosticAsyncAppenderTest {
    @Test
    void slowDiskAndFullQueueDoNotBlockProducersAndDropsAreCounted() throws Exception {
        var context = new LoggerContext();
        context.setMDCAdapter(new ch.qos.logback.classic.util.LogbackMDCAdapter());
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var sink =
                new AppenderBase<ILoggingEvent>() {
                    @Override
                    protected void append(ILoggingEvent event) {
                        entered.countDown();
                        try {
                            release.await(5, TimeUnit.SECONDS);
                        } catch (InterruptedException ex) {
                            Thread.currentThread().interrupt();
                        }
                    }
                };
        sink.setContext(context);
        sink.setName("slow-disk");
        sink.start();
        var async = new DiagnosticAsyncAppender();
        async.setContext(context);
        async.setQueueSize(2);
        async.addAppender(sink);
        async.start();
        try {
            long before = DiagnosticAsyncAppender.droppedEvents();
            var logger = context.getLogger("diagnostic-test");
            async.doAppend(new LoggingEvent("test", logger, Level.INFO, "first", null, null));
            assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
            org.junit.jupiter.api.Assertions.assertTimeoutPreemptively(
                    java.time.Duration.ofSeconds(1),
                    () -> {
                        for (int i = 0; i < 20; i++)
                            async.doAppend(
                                    new LoggingEvent(
                                            "test", logger, Level.ERROR, "next", null, null));
                    });
            assertThat(async.isNeverBlock()).isTrue();
            assertThat(async.getNumberOfElementsInQueue()).isEqualTo(2);
            assertThat(DiagnosticAsyncAppender.droppedEvents() - before).isEqualTo(18);
        } finally {
            release.countDown();
            async.stop();
            context.stop();
        }
    }
}
