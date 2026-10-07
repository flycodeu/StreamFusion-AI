package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.access.adapter.*;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.access.service.*;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class CameraAccessWorkerTest {
    @Test
    void resubmitsNextPhaseWhenStartArrivesBetweenCompleteCommitAndWorkerFinally()
            throws Exception {
        var jobs = mock(CameraAccessJobsService.class);
        var rules = mock(CameraConnectionRules.class);
        var registry = mock(CameraAccessAdapterRegistry.class);
        var bulk = mock(CameraBulkImportService.class);
        var worker =
                new CameraAccessWorker(jobs, rules, registry, mock(CameraNetworkScan.class), bulk);
        var firstConnection =
                new CameraConnection(
                        "RTSP",
                        "rtsp",
                        "10.0.1.5",
                        554,
                        "rtsp",
                        null,
                        null,
                        554,
                        "test",
                        null,
                        null,
                        List.of("rtsp://10.0.1.5/main"));
        var first =
                new CameraAccessJobsService.Work(
                        123,
                        1,
                        91,
                        "trace",
                        new CameraAccessJobsService.Payload("session", firstConnection, null));
        var nextConnection =
                new CameraConnection(
                        "HIK_PLATFORM",
                        "platform",
                        "10.0.1.5",
                        80,
                        "http",
                        "key",
                        "secret",
                        554,
                        "test",
                        "7",
                        "0",
                        null,
                        1,
                        100);
        var second =
                new CameraAccessJobsService.Work(
                        123,
                        4,
                        91,
                        "trace",
                        new CameraAccessJobsService.Payload(
                                "session",
                                nextConnection,
                                null,
                                null,
                                null,
                                new CameraAccessJobsService.BulkControl(
                                        null, "scope", null, null)));
        var queued = new java.util.concurrent.atomic.AtomicBoolean(false);
        var committed = new CountDownLatch(1);
        var releaseFinally = new CountDownLatch(1);
        var nextCompleted = new CountDownLatch(1);
        when(jobs.claim(123)).thenReturn(first, second);
        when(jobs.queued(123)).thenAnswer(i -> queued.get());
        var firstCatalog = new CameraAccessCatalog("RTSP", null, List.of(), true, List.of());
        when(rules.rtspCatalog(firstConnection)).thenReturn(firstCatalog);
        doAnswer(
                        i -> {
                            queued.set(true);
                            committed.countDown();
                            assertThat(releaseFinally.await(5, TimeUnit.SECONDS)).isTrue();
                            return null;
                        })
                .when(jobs)
                .complete(first, firstCatalog);
        var secondCatalog =
                new CameraAccessCatalog(
                        "HIK_PLATFORM",
                        null,
                        List.of(),
                        true,
                        List.of(),
                        new CameraAccessCatalog.Page(1, 100, 0L, false));
        when(registry.discover(eq("HIK_PLATFORM"), any())).thenReturn(secondCatalog);
        when(bulk.beginPage(second, secondCatalog)).thenReturn(second);
        when(bulk.finishPage(second, secondCatalog))
                .thenAnswer(
                        i -> {
                            queued.set(false);
                            nextCompleted.countDown();
                            return null;
                        });
        try {
            worker.submit(123);
            assertThat(committed.await(5, TimeUnit.SECONDS)).isTrue();
            worker.submit(123); // The previous worker still owns the in-memory submission slot.
            releaseFinally.countDown();
            assertThat(nextCompleted.await(5, TimeUnit.SECONDS)).isTrue();
            verify(jobs, times(2)).claim(123);
            verify(bulk, times(1)).finishPage(second, secondCatalog);
            verify(jobs, never()).failQueued(anyLong(), anyString());
        } finally {
            releaseFinally.countDown();
            worker.stop();
        }
    }

    @Test
    void platformBulkAutomaticallyReadsAllPagesWithOneBoundedCatalogAtATime() throws Exception {
        var jobs = mock(CameraAccessJobsService.class);
        var rules = mock(CameraConnectionRules.class);
        var registry = mock(CameraAccessAdapterRegistry.class);
        var bulk = mock(CameraBulkImportService.class);
        var worker =
                new CameraAccessWorker(jobs, rules, registry, mock(CameraNetworkScan.class), bulk);
        var connection =
                new CameraConnection(
                        "HIK_PLATFORM",
                        "platform",
                        "10.0.1.5",
                        80,
                        "http",
                        "key",
                        "secret",
                        554,
                        "test",
                        "7",
                        "0",
                        null,
                        1,
                        100);
        var work =
                new CameraAccessJobsService.Work(
                        123,
                        4,
                        91,
                        "trace",
                        new CameraAccessJobsService.Payload(
                                "session",
                                connection,
                                null,
                                null,
                                null,
                                new CameraAccessJobsService.BulkControl(
                                        null, "fingerprint", null, null)));
        when(jobs.claim(123)).thenReturn(work);
        when(rules.endpoint(connection)).thenReturn(java.net.URI.create("http://10.0.1.5/"));
        when(registry.discover(eq("HIK_PLATFORM"), any()))
                .thenAnswer(
                        invocation -> {
                            CameraAccessContext context = invocation.getArgument(1);
                            assertThat(context.pageSize()).isEqualTo(100);
                            int page = context.pageNumber();
                            var entries =
                                    java.util.stream.IntStream.range(0, page == 1 ? 100 : 1)
                                            .mapToObj(
                                                    i ->
                                                            new CameraAccessCatalog.Channel(
                                                                    "key-" + page + "-" + i,
                                                                    "name",
                                                                    List.of(),
                                                                    false))
                                            .toList();
                            return new CameraAccessCatalog(
                                    "HIK_PLATFORM",
                                    null,
                                    entries,
                                    true,
                                    List.of(),
                                    new CameraAccessCatalog.Page(page, 100, 101L, page == 1));
                        });
        when(bulk.beginPage(any(), any())).thenAnswer(invocation -> invocation.getArgument(0));
        when(bulk.importItem(any(), any(), anyInt()))
                .thenAnswer(invocation -> invocation.getArgument(0));
        var finished = new CountDownLatch(1);
        when(bulk.finishPage(any(), any()))
                .thenAnswer(
                        invocation -> {
                            CameraAccessCatalog catalog = invocation.getArgument(1);
                            if (catalog.page().hasMore()) return invocation.getArgument(0);
                            finished.countDown();
                            return null;
                        });
        try {
            worker.submit(123);
            assertThat(finished.await(5, TimeUnit.SECONDS)).isTrue();
            verify(registry, times(2)).discover(eq("HIK_PLATFORM"), any());
            verify(bulk, times(101)).importItem(any(), any(), anyInt());
            verify(jobs, never()).complete(any(), any());
            verify(jobs, never()).fail(anyLong(), any(), anyString());
        } finally {
            worker.stop();
        }
    }

    @Test
    void actorRevokedBeforeClaimCanTerminateRequeuedBulkWithNonzeroVersion() {
        var jobs = mock(CameraAccessJobsService.class);
        var worker =
                new CameraAccessWorker(
                        jobs,
                        mock(CameraConnectionRules.class),
                        mock(CameraAccessAdapterRegistry.class),
                        mock(CameraNetworkScan.class),
                        mock(CameraBulkImportService.class));
        when(jobs.claim(123)).thenThrow(new CameraAdapterException("ACTOR_REVOKED"));
        try {
            worker.submit(123);
            verify(jobs, timeout(5000)).failQueued(123, "ACTOR_REVOKED");
        } finally {
            worker.stop();
        }
    }

    @Test
    void duplicateDispatchCannotFillQueueAndFailAlreadyRunningJob() throws Exception {
        var jobs = mock(CameraAccessJobsService.class);
        var rules = mock(CameraConnectionRules.class);
        var registry = mock(CameraAccessAdapterRegistry.class);
        var worker =
                new CameraAccessWorker(
                        jobs,
                        rules,
                        registry,
                        mock(CameraNetworkScan.class),
                        mock(CameraBulkImportService.class));
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var connection =
                new CameraConnection(
                        "RTSP",
                        "test",
                        "10.0.1.5",
                        554,
                        "rtsp",
                        null,
                        null,
                        554,
                        "test",
                        null,
                        null,
                        List.of("rtsp://10.0.1.5/main"));
        var work =
                new CameraAccessJobsService.Work(
                        123L,
                        1L,
                        91L,
                        "test-trace",
                        new CameraAccessJobsService.Payload("session", connection, null));
        when(jobs.claim(123L))
                .thenAnswer(
                        invocation -> {
                            entered.countDown();
                            assertThat(release.await(5, TimeUnit.SECONDS)).isTrue();
                            return work;
                        });
        var catalog = new CameraAccessCatalog("RTSP", null, List.of(), true, List.of());
        when(rules.rtspCatalog(connection)).thenReturn(catalog);
        try {
            worker.submit(123L);
            assertThat(entered.await(5, TimeUnit.SECONDS)).isTrue();
            for (int i = 0; i < 40; i++) worker.submit(123L);
            verify(jobs, never()).fail(anyLong(), any(), anyString());
            release.countDown();
            verify(jobs, timeout(5000)).complete(work, catalog);
            verify(jobs, times(1)).claim(123L);
        } finally {
            release.countDown();
            worker.stop();
        }
    }
}
