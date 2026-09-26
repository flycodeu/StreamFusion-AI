package com.streamfusion.platform.server.service;

import static org.assertj.core.api.Assertions.assertThat;
import static org.awaitility.Awaitility.await;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.server.config.ServerMonitorProperties;
import com.streamfusion.platform.server.pojo.vo.ServerSnapshotVo;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneId;
import java.time.ZoneOffset;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class ServerMonitorServiceTest {
    @Test
    void sharesOneCollectionAndCachesThenFallsBackToStaleSample() throws Exception {
        ServerCollector collector = mock(ServerCollector.class);
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var clock = new MutableClock();
        var sample = sample(clock.instant());
        when(collector.collect())
                .thenAnswer(
                        call -> {
                            entered.countDown();
                            release.await(2, TimeUnit.SECONDS);
                            return sample;
                        });
        var service = new ServerMonitorService(collector, properties(), clock);
        try {
            var first = service.snapshot();
            assertThat(entered.await(1, TimeUnit.SECONDS)).isTrue();
            assertThat(service.snapshot()).isSameAs(first);
            release.countDown();
            assertThat(first.get(1, TimeUnit.SECONDS)).isEqualTo(sample);
            assertThat(service.snapshot().get()).isEqualTo(sample);
            verify(collector, times(1)).collect();
            clock.now = clock.now.plusSeconds(31);
            when(collector.collect()).thenThrow(new IllegalStateException("private error"));
            var fallback = service.snapshot().get(1, TimeUnit.SECONDS);
            assertThat(fallback.stale()).isTrue();
            assertThat(fallback.state()).isEqualTo("UNAVAILABLE");
            assertThat(fallback.sampledAt()).isEqualTo(sample.sampledAt());
            assertThat(service.snapshot().get().stale()).isTrue();
            verify(collector, times(2)).collect();
        } finally {
            release.countDown();
            service.close();
        }
    }

    @Test
    void boundsInitialResponseAndDoesNotQueueMoreWorkBehindAHungProbe() throws Exception {
        ServerCollector collector = mock(ServerCollector.class);
        var release = new CountDownLatch(1);
        when(collector.collect())
                .thenAnswer(
                        call -> {
                            while (release.getCount() > 0) {
                                try {
                                    release.await(5, TimeUnit.SECONDS);
                                } catch (InterruptedException ignored) {
                                }
                            }
                            return sample(Instant.now());
                        });
        var service = new ServerMonitorService(collector, properties(), Clock.systemUTC());
        try {
            var future = service.snapshot();
            assertThat(future.get(3, TimeUnit.SECONDS).state()).isEqualTo("UNAVAILABLE");
            assertThat(service.snapshot()).isSameAs(future);
            verify(collector, times(1)).collect();
        } finally {
            release.countDown();
            service.close();
        }
    }

    @Test
    void deadlineInterruptsCollectionDiscardsLateSuccessAndRecoversOnNextRefresh()
            throws Exception {
        ServerCollector collector = mock(ServerCollector.class);
        var interrupted = new CountDownLatch(1);
        var clock = new MutableClock();
        var lateSample = sample(clock.instant());
        when(collector.collect())
                .thenAnswer(
                        call -> {
                            try {
                                Thread.sleep(10_000);
                            } catch (InterruptedException ignored) {
                                interrupted.countDown();
                            }
                            return lateSample;
                        });
        var service = new ServerMonitorService(collector, properties(), clock);
        try {
            var first = service.snapshot();
            assertThat(first.get(3, TimeUnit.SECONDS).state()).isEqualTo("UNAVAILABLE");
            assertThat(interrupted.await(1, TimeUnit.SECONDS)).isTrue();
            await().atMost(Duration.ofSeconds(1)).until(() -> service.snapshot() != first);
            assertThat(service.snapshot().get().sampledAt()).isNull();
            clock.now = clock.now.plusSeconds(31);
            var fresh = sample(clock.instant());
            doReturn(fresh).when(collector).collect();
            assertThat(service.snapshot().get(1, TimeUnit.SECONDS)).isEqualTo(fresh);
            verify(collector, times(2)).collect();
        } finally {
            service.close();
        }
    }

    private static ServerSnapshotVo sample(Instant time) {
        return new ServerSnapshotVo(
                time, false, "READY", null, null, List.of(), "UNAVAILABLE", List.of());
    }

    private static ServerMonitorProperties properties() {
        return new ServerMonitorProperties(
                Duration.ofSeconds(30),
                Duration.ofMillis(100),
                Duration.ofSeconds(2),
                "127.0.0.1",
                0,
                "127.0.0.1",
                0,
                "127.0.0.1",
                0,
                false);
    }

    private static final class MutableClock extends Clock {
        private Instant now = Instant.parse("2026-09-26T00:00:00Z");

        @Override
        public ZoneId getZone() {
            return ZoneOffset.UTC;
        }

        @Override
        public Clock withZone(ZoneId zone) {
            return this;
        }

        @Override
        public Instant instant() {
            return now;
        }
    }
}
