package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.*;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.execution.*;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.previewnode.PreviewControlTestFixture;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.DataSourceTransactionManager;

class PreviewSchedulerTest {
    @TempDir Path directory;
    final ObjectMapper json = new ObjectMapper().findAndRegisterModules();
    final LocalPreviewExecutor local = mock(LocalPreviewExecutor.class);
    JdbcTemplate sql;
    PreviewNodeRepository repository;
    PreviewReservationLedger ledger;
    PreviewNodeManager manager;
    PreviewScheduler scheduler;
    PreviewRoutingExecutor routing;

    void initialize() throws Exception {
        javax.sql.DataSource ds;
        if ("true".equals(System.getenv("SF_PREVIEW_MYSQL_TEST"))) {
            String database = "sf_s3_jdbc_" + UUID.randomUUID().toString().replace("-", "");
            String origin = "jdbc:mysql://127.0.0.1:33317/";
            try (var connection = java.sql.DriverManager.getConnection(origin, "root", "");
                    var statement = connection.createStatement()) {
                statement.execute("CREATE DATABASE " + database);
            }
            ds =
                    new org.springframework.jdbc.datasource.DriverManagerDataSource(
                            origin + database, "root", "");
            var schema =
                    new org.springframework.jdbc.datasource.init.ResourceDatabasePopulator(
                            new org.springframework.core.io.FileSystemResource(
                                    "sql/streamfusion-mysql.sql"));
            schema.setSqlScriptEncoding("UTF-8");
            schema.execute(ds);
        } else {
            var h2 = new JdbcDataSource();
            h2.setURL(
                    "jdbc:h2:mem:"
                            + UUID.randomUUID()
                            + ";MODE=MySQL;DB_CLOSE_DELAY=-1;LOCK_TIMEOUT=10000");
            IdentitySchema.initialize(h2);
            ds = h2;
        }
        sql = new JdbcTemplate(ds);
        repository = new PreviewNodeRepository(sql);
        ledger =
                new PreviewReservationLedger(sql, repository, new DataSourceTransactionManager(ds));
        manager =
                new PreviewNodeManager(
                        repository,
                        new PreviewNodeProperties("", ""),
                        new PreviewProperties(64, 64),
                        json);
        scheduler = new PreviewScheduler(repository, ledger, manager, local);
        routing =
                new PreviewRoutingExecutor(
                        local,
                        new RemotePreviewExecutor(repository, manager, json),
                        manager,
                        repository,
                        scheduler);
        when(local.configurationVersion()).thenReturn("local");
        when(local.start(anyString(), anyList(), any(), any()))
                .thenReturn(new PreviewExecutor.Playback("H264", false));
    }

    PreviewDeadline deadline() {
        return PreviewDeadline.after(Duration.ofSeconds(15));
    }

    PreviewBudget budget(int sources, int viewers) {
        return new PreviewBudget(sources, viewers, 64, 4, 655360, 655360, 8192, 8192);
    }

    void policy(String id, String mode, PreviewBudget budget, int priority) {
        var node = repository.node(id);
        repository.policy(id, mode, priority, budget, node.version());
    }

    void register(PreviewControlTestFixture node, String id) {
        repository.register(
                new PreviewNodeConnection(id, id, node.origin, node.pin, node.token, "127.0.0.1"),
                node.service.snapshot().configVersion(),
                null);
        policy(id, "REMOTE", budget(12, 64), 100);
    }

    String open(long profile) {
        String id = UUID.randomUUID().toString();
        var key = new PreviewExecutor.SourceKey(profile, "v1");
        routing.prepare(id, key, List.of("H264"), deadline());
        routing.attachSource(
                id, key, () -> URI.create("rtsp://192.0.2.10:554/live"), () -> {}, deadline());
        routing.start(id, List.of("H264"), () -> {}, deadline());
        routing.confirmReady(id, deadline());
        return id;
    }

    @Test
    void fillsAThenBAndReusesTheOriginalSourceWithoutMigration() throws Exception {
        initialize();
        policy("local", "LOCAL", budget(64, 64), 0);
        try (var a = new PreviewControlTestFixture(directory.resolve("a"), "a");
                var b = new PreviewControlTestFixture(directory.resolve("b"), "b")) {
            register(a, "a");
            register(b, "b");
            List<String> ids = new ArrayList<>();
            for (int i = 1; i <= 20; i++) ids.add(open(i));
            assertThat(ledger.usage("a").sources()).isEqualTo(12);
            assertThat(ledger.usage("b").sources()).isEqualTo(8);
            String shared = open(1);
            ids.add(shared);
            assertThat(repository.viewer(shared).nodeId()).isEqualTo("a");
            assertThat(ledger.usage("a").sources()).isEqualTo(12);
            assertThat(ledger.usage("a").viewers()).isEqualTo(13);
            // B disappears; A's sessions continue and a source assigned to B does not move to A.
            b.server.close();
            manager.invalidate("b");
            routing.renew(shared, deadline());
            assertThatThrownBy(() -> open(13)).isInstanceOf(PreviewFailure.class);
            assertThat(ledger.source(13).nodeId()).isEqualTo("b");
            policy("a", "DRAINING", budget(12, 64), 100);
            routing.renew(shared, deadline());
            assertThatThrownBy(() -> open(1)).hasMessage("NODE_CONFIGURATION_CHANGED");
            for (String id : ids)
                if (repository.viewer(id).nodeId().equals("a")) routing.close(id, deadline());
            assertThat(ledger.usage("a")).isEqualTo(PreviewBudget.Usage.empty());
        }
    }

    @Test
    void hundredConcurrentRequestsCannotOversubscribeLastViewer() throws Exception {
        initialize();
        policy("local", "REMOTE", budget(64, 1), 0);
        var start = new CountDownLatch(1);
        var successes = new AtomicInteger();
        try (var workers = Executors.newFixedThreadPool(24)) {
            var futures = new ArrayList<Future<?>>();
            for (int i = 0; i < 100; i++) {
                final int profile = i + 1;
                futures.add(
                        workers.submit(
                                () -> {
                                    try {
                                        start.await();
                                        routing.prepare(
                                                UUID.randomUUID().toString(),
                                                new PreviewExecutor.SourceKey((long) profile, "v1"),
                                                List.of("H264"),
                                                deadline());
                                        successes.incrementAndGet();
                                    } catch (PreviewFailure expected) {
                                        assertThat(expected.reason()).isEqualTo("PREVIEW_CAPACITY");
                                    } catch (InterruptedException e) {
                                        throw new RuntimeException(e);
                                    }
                                }));
            }
            start.countDown();
            for (var future : futures) future.get(30, TimeUnit.SECONDS);
        }
        assertThat(successes).hasValue(1);
        assertThat(ledger.usage("local").viewers()).isOne();
        verify(local, times(1)).prepare(anyString());
    }

    @Test
    void uncertainCleanupRetainsSourceOutputAndViewerUntilConfirmed() throws Exception {
        initialize();
        var id = open(1);
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(local).close(eq(id), any());
        assertThatThrownBy(() -> routing.close(id, deadline())).hasMessage("CLEANUP_PENDING");
        assertThat(ledger.usage("local").sources()).isOne();
        assertThat(ledger.usage("local").viewers()).isOne();
        assertThatThrownBy(() -> open(1)).hasMessage("CLEANUP_PENDING");
        assertThatThrownBy(
                        () ->
                                routing.prepare(
                                        UUID.randomUUID().toString(),
                                        new PreviewExecutor.SourceKey(1L, "v2"),
                                        List.of("H264"),
                                        deadline()))
                .hasMessage("SOURCE_GENERATION_PENDING");
        doNothing().when(local).close(eq(id), any());
        routing.close(id, deadline());
        assertThat(ledger.usage("local")).isEqualTo(PreviewBudget.Usage.empty());
        assertThat(sql.queryForObject("SELECT COUNT(*) FROM preview_output", Integer.class))
                .isZero();
    }

    @Test
    void sharedOutputCostsOneTranscodeAndEgressRefusesSameSourceSpill() throws Exception {
        initialize();
        when(local.start(anyString(), anyList(), any(), any()))
                .thenReturn(new PreviewExecutor.Playback("H264", true, "H265"));
        policy("local", "REMOTE", new PreviewBudget(64, 64, 1, 4, 655360, 20480, 8192, 8192), 0);
        String first = open(1), second = open(1);
        assertThat(ledger.usage("local").transcodes()).isOne();
        assertThat(ledger.usage("local").sources()).isOne();
        assertThatThrownBy(() -> open(1)).hasMessage("NODE_EGRESS_CAPACITY");
        routing.close(first, deadline());
        assertThat(ledger.usage("local").transcodes()).isOne();
        routing.close(second, deadline());
        assertThat(ledger.usage("local").transcodes()).isZero();
    }

    @Test
    void platformRestartKeepsOtherNodeAvailableAndCleansOnlyOriginalOwnership() throws Exception {
        initialize();
        String id = open(1);
        var restarted = new PreviewScheduler(repository, ledger, manager, local);
        var executor =
                new PreviewRoutingExecutor(
                        local,
                        new RemotePreviewExecutor(repository, manager, json),
                        manager,
                        repository,
                        restarted);
        assertThat(executor.retainedViewers(java.util.Set.of())).isOne();
        assertThat(executor.retainedViewers(java.util.Set.of(id))).isZero();
        assertThatThrownBy(
                        () ->
                                executor.prepare(
                                        UUID.randomUUID().toString(),
                                        new PreviewExecutor.SourceKey(2L, "v1"),
                                        List.of("H264"),
                                        deadline()))
                .hasMessage("NODE_RECONCILING");
        executor.reconcile(() -> false);
        assertThat(repository.viewer(id)).isNull();
        assertThat(executor.retainedViewers(java.util.Set.of())).isZero();
        assertThat(ledger.source(1)).isNull();
        verify(local).close(eq(id), any());
    }

    @Test
    void onlyLocalStillSupports64AndMultipleNodesLiftTheGlobal64Limit() throws Exception {
        initialize();
        assertThat(routing.sessionLimit(64)).isEqualTo(64);
        try (var a = new PreviewControlTestFixture(directory.resolve("a"), "a")) {
            register(a, "a");
            policy("a", "REMOTE", budget(64, 64), 100);
            assertThat(routing.sessionLimit(64)).isEqualTo(128);
            for (int i = 0; i < 65; i++) open(i + 1);
            assertThat(ledger.usage("local").viewers()).isEqualTo(64);
            assertThat(ledger.usage("a").viewers()).isOne();
        }
    }

    @Test
    void lostCreateAndQueryResponsesNeverCauseASecondNodeToPull() throws Exception {
        initialize();
        policy("local", "LOCAL", budget(64, 64), 0);
        try (var a = new PreviewControlTestFixture(directory.resolve("a"), "a");
                var b = new PreviewControlTestFixture(directory.resolve("b"), "b")) {
            register(a, "a");
            register(b, "b");
            var faulty = spy(manager);
            doAnswer(
                            call -> {
                                var client = spy(a.client());
                                doAnswer(
                                                request -> {
                                                    request.callRealMethod();
                                                    throw new PreviewFailure("NODE_UNREACHABLE");
                                                })
                                        .when(client)
                                        .apply(any(), any());
                                doThrow(new PreviewFailure("NODE_UNREACHABLE"))
                                        .when(client)
                                        .get(
                                                startsWith("operations/"),
                                                eq(
                                                        com.streamfusion.platform.media.preview
                                                                .control.PreviewControl.Receipt
                                                                .class),
                                                any());
                                return client;
                            })
                    .when(faulty)
                    .client(argThat(c -> c.nodeId().equals("a")));
            var selected = new PreviewScheduler(repository, ledger, faulty, local);
            var remote = new RemotePreviewExecutor(repository, faulty, json);
            var route = new PreviewRoutingExecutor(local, remote, faulty, repository, selected);
            String id = UUID.randomUUID().toString();
            var key = new PreviewExecutor.SourceKey(1L, "v1");
            route.prepare(id, key, List.of("H264"), deadline());
            route.attachSource(
                    id, key, () -> URI.create("rtsp://192.0.2.10:554/live"), () -> {}, deadline());
            assertThatThrownBy(() -> route.start(id, List.of("H264"), () -> {}, deadline()))
                    .hasMessage("NODE_UNREACHABLE");
            assertThat(repository.viewer(id).nodeId()).isEqualTo("a");
            assertThat(ledger.usage("a").viewers()).isOne();
            assertThat(ledger.usage("a").transcodes()).isOne();
            verify(a.core, times(1)).prepare(id);
            verify(b.core, never()).prepare(anyString());
            // Failed STOP response also keeps the reservation; no expiry-only release.
            assertThatThrownBy(() -> route.close(id, deadline())).hasMessage("NODE_UNREACHABLE");
            assertThat(repository.viewer(id)).isNotNull();
        }
    }

    @Test
    void localConfigurationChangeWaitsForConfirmedCleanup() throws Exception {
        initialize();
        String id = open(1);
        when(local.configurationVersion()).thenReturn("d".repeat(64));
        assertThatThrownBy(() -> open(2)).hasMessage("NODE_CONFIGURATION_CHANGED");
        assertThat(repository.viewer(id).configVersion()).isEqualTo("local");
        routing.renew(id, deadline());
        routing.close(id, deadline());
        String replacement = open(2);
        assertThat(repository.viewer(replacement).configVersion()).isEqualTo("d".repeat(64));
    }
}
