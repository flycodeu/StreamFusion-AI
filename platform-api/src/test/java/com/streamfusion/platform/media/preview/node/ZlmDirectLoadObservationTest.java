package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.doAnswer;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import java.net.URI;
import java.nio.file.Path;
import java.time.Instant;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class ZlmDirectLoadObservationTest {
    @TempDir Path directory;

    @Test
    void slowFailedMetricDoesNotDelayHealthOrChangeReadyState() throws Exception {
        var gateway = mock(ZlmDirectGateway.class);
        var node = node();
        var repository = repository(node);
        var scheduler = scheduler(node);
        var metricStarted = new CountDownLatch(1);
        var releaseMetric = new CountDownLatch(1);
        when(gateway.version(any(), any())).thenReturn("v1");
        doAnswer(
                        ignored -> {
                            metricStarted.countDown();
                            releaseMetric.await(2, TimeUnit.SECONDS);
                            throw new PreviewFailure("ZLM_REQUEST_FAILED");
                        })
                .when(gateway)
                .networkThreadLoad(any(), any());
        var manager = manager(repository, gateway);
        try {
            long started = System.nanoTime();
            manager.check();
            assertThat(TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started)).isLessThan(1500);
            assertThat(metricStarted.await(1, TimeUnit.SECONDS)).isTrue();
            var status = manager.statuses(scheduler).get(0);
            assertThat(status.state()).isEqualTo("READY");
            assertThat(status.reasonCode()).isNull();
            assertThat(status.zlmLoad()).isNull();
            assertThat(status.usage()).isEqualTo(PreviewBudget.Usage.empty());
        } finally {
            releaseMetric.countDown();
            manager.closeLoadWorkers();
        }
    }

    @Test
    void successfulMetricIsCachedOnlyForTheObservedNode() throws Exception {
        var gateway = mock(ZlmDirectGateway.class);
        var node = node();
        var repository = repository(node);
        var scheduler = scheduler(node);
        var sample = new ZlmDirectGateway.NetworkThreadLoad(2, 40, 60, Instant.now());
        when(gateway.version(any(), any())).thenReturn("v1");
        when(gateway.networkThreadLoad(any(), any())).thenReturn(sample);
        var manager = manager(repository, gateway);
        try {
            manager.check();
            long until = System.nanoTime() + TimeUnit.SECONDS.toNanos(2);
            while (manager.statuses(scheduler).get(0).zlmLoad() == null
                    && System.nanoTime() < until) Thread.sleep(10);
            assertThat(manager.statuses(scheduler).get(0).zlmLoad()).isEqualTo(sample);
            manager.invalidate(node.nodeId());
            assertThat(manager.statuses(scheduler).get(0).zlmLoad()).isNull();
        } finally {
            manager.closeLoadWorkers();
        }
    }

    @Test
    void invalidationRejectsAnInFlightSampleBeforeTheNextObservation() throws Exception {
        var gateway = mock(ZlmDirectGateway.class);
        var node = node();
        var repository = repository(node);
        var scheduler = scheduler(node);
        var firstStarted = new CountDownLatch(1);
        var releaseFirst = new CountDownLatch(1);
        var secondStarted = new CountDownLatch(1);
        var releaseSecond = new CountDownLatch(1);
        var calls = new AtomicInteger();
        var oldSample = new ZlmDirectGateway.NetworkThreadLoad(1, 90, 90, Instant.now());
        var newSample = new ZlmDirectGateway.NetworkThreadLoad(1, 10, 10, Instant.now());
        when(gateway.version(any(), any())).thenReturn("v1");
        doAnswer(
                        ignored -> {
                            if (calls.incrementAndGet() == 1) {
                                firstStarted.countDown();
                                releaseFirst.await(2, TimeUnit.SECONDS);
                                return oldSample;
                            }
                            secondStarted.countDown();
                            releaseSecond.await(2, TimeUnit.SECONDS);
                            return newSample;
                        })
                .when(gateway)
                .networkThreadLoad(any(), any());
        var manager = manager(repository, gateway);
        try {
            manager.check();
            assertThat(firstStarted.await(1, TimeUnit.SECONDS)).isTrue();
            manager.invalidate(node.nodeId());
            releaseFirst.countDown();
            long until = System.nanoTime() + TimeUnit.SECONDS.toNanos(2);
            while (secondStarted.getCount() > 0 && System.nanoTime() < until) {
                manager.check();
                Thread.sleep(10);
            }
            assertThat(secondStarted.getCount()).isZero();
            assertThat(manager.statuses(scheduler).get(0).zlmLoad()).isNull();
            releaseSecond.countDown();
            until = System.nanoTime() + TimeUnit.SECONDS.toNanos(2);
            while (manager.statuses(scheduler).get(0).zlmLoad() == null
                    && System.nanoTime() < until) Thread.sleep(10);
            assertThat(manager.statuses(scheduler).get(0).zlmLoad()).isEqualTo(newSample);
        } finally {
            releaseFirst.countDown();
            releaseSecond.countDown();
            manager.closeLoadWorkers();
        }
    }

    private PreviewNodeRepository.Node node() {
        var connection =
                new ZlmDirectConnection(
                        "zlm-1234567890abcdef",
                        URI.create("http://127.0.0.1:8080"),
                        directory.resolve("secret"),
                        "127.0.0.1");
        return new PreviewNodeRepository.Node(
                null, "REMOTE", "v1", 1, PreviewBudget.defaults(64, 0), 100, connection);
    }

    private PreviewNodeRepository repository(PreviewNodeRepository.Node node) {
        var repository = mock(PreviewNodeRepository.class);
        when(repository.nodes()).thenReturn(List.of(node));
        when(repository.viewers()).thenReturn(List.of());
        return repository;
    }

    private PreviewScheduler scheduler(PreviewNodeRepository.Node node) {
        var scheduler = mock(PreviewScheduler.class);
        when(scheduler.capacity(node.nodeId()))
                .thenReturn(
                        new PreviewScheduler.PreviewBudgetView(PreviewBudget.Usage.empty(), null));
        return scheduler;
    }

    private PreviewNodeManager manager(PreviewNodeRepository repository, ZlmDirectGateway gateway) {
        return new PreviewNodeManager(
                repository,
                new PreviewNodeProperties("", ""),
                new PreviewProperties(64, 64),
                new ObjectMapper(),
                gateway);
    }
}
