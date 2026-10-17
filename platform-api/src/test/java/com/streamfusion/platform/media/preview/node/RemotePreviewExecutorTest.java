package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor.SourceKey;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.previewnode.PreviewControlTestFixture;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.UUID;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;

class RemotePreviewExecutorTest {
    @TempDir Path directory;

    PreviewDeadline budget() {
        return PreviewDeadline.after(Duration.ofSeconds(10));
    }

    @Test
    void roundTripPinsOwnershipAndDrainsBeforeReturningToLocal() throws Exception {
        try (var node = new PreviewControlTestFixture(directory)) {
            var dataSource = new JdbcDataSource();
            dataSource.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
            IdentitySchema.initialize(dataSource);
            var sql = new JdbcTemplate(dataSource);
            var repository = new PreviewNodeRepository(sql);
            Path config = directory.resolve("registration.json");
            node.json.writeValue(
                    config.toFile(),
                    new PreviewNodeConnection(
                            "test-a", "测试节点", node.origin, node.pin, node.token, "127.0.0.1"));
            var manager =
                    new PreviewNodeManager(
                            repository,
                            new PreviewNodeProperties(config.toString()),
                            new PreviewProperties(64, 64),
                            node.json);
            var registration = manager.register(manager.registration());
            assertThat(registration.mode()).isEqualTo("LOCAL");
            manager.localPrepared("local-viewer");
            assertThatThrownBy(
                            () ->
                                    manager.changeMode(
                                            manager.checkMode("REMOTE", registration.version())))
                    .hasMessage("NODE_DRAIN_REQUIRED");
            manager.localClosed("local-viewer");
            var enabled = manager.changeMode(manager.checkMode("REMOTE", registration.version()));
            var remote = new RemotePreviewExecutor(repository, manager, node.json);
            String id = UUID.randomUUID().toString();
            remote.prepare(id);
            remote.attachSource(
                    id,
                    new SourceKey(2L, "source-v1"),
                    () -> URI.create("rtsp://camera:secret@192.0.2.10:554/live"),
                    () -> {},
                    budget());
            assertThat(remote.start(id, List.of("H264"), () -> {}, budget()).codec())
                    .isEqualTo("H264");
            remote.confirmReady(id, budget());
            assertThat(repository.viewer(id).bootId()).isEqualTo(node.service.snapshot().bootId());
            assertThat(sql.queryForList("SELECT * FROM preview_operation").toString())
                    .doesNotContain("rtsp://", "secret");
            var draining = manager.changeMode(manager.checkMode("DRAINING", enabled.version()));
            remote.renew(id, budget());
            assertThatThrownBy(() -> remote.prepare(UUID.randomUUID().toString()))
                    .hasMessage("NODE_DRAINING");
            assertThatThrownBy(
                            () ->
                                    manager.changeMode(
                                            manager.checkMode("LOCAL", draining.version())))
                    .hasMessage("NODE_DRAIN_REQUIRED");
            remote.close(id, budget());
            assertThat(repository.viewers()).isEmpty();
            var refreshed = manager.register(manager.registration());
            assertThat(refreshed.mode()).isEqualTo("DRAINING");
            assertThat(refreshed.version()).isGreaterThan(draining.version());
            assertThat(manager.changeMode(manager.checkMode("LOCAL", refreshed.version())).mode())
                    .isEqualTo("LOCAL");
            verify(node.core).close(eq(id), any());
        }
    }

    @Test
    void platformRestartReconcilesOriginalNodeWithoutRecreatingMedia() throws Exception {
        try (var node = new PreviewControlTestFixture(directory)) {
            var ds = new JdbcDataSource();
            ds.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
            IdentitySchema.initialize(ds);
            var repository = new PreviewNodeRepository(new JdbcTemplate(ds));
            var connection =
                    new PreviewNodeConnection(
                            "test-a", "A", node.origin, node.pin, node.token, "127.0.0.1");
            repository.register(connection, node.service.snapshot().configVersion(), null);
            repository.mode("REMOTE", 0);
            var manager =
                    new PreviewNodeManager(
                            repository,
                            new PreviewNodeProperties(""),
                            new PreviewProperties(64, 64),
                            node.json);
            var first = new RemotePreviewExecutor(repository, manager, node.json);
            String id = UUID.randomUUID().toString();
            first.prepare(id);
            first.attachSource(
                    id,
                    new SourceKey(1L, "v1"),
                    () -> URI.create("rtsp://192.0.2.10:554/live"),
                    () -> {},
                    budget());
            first.start(id, List.of("H264"), () -> {}, budget());
            var restarted = new RemotePreviewExecutor(repository, manager, node.json);
            assertThatThrownBy(restarted::requireRecovered).hasMessage("NODE_RECONCILING");
            restarted.reconcile(() -> false);
            assertThat(repository.viewers()).isEmpty();
            verify(node.core, times(1)).prepare(id);
            verify(node.core, times(1)).close(eq(id), any());
        }
    }

    @Test
    void lostCreateResponseQueriesOriginalOperationWithoutRepeatingCreate() throws Exception {
        try (var node = new PreviewControlTestFixture(directory)) {
            var ds = new JdbcDataSource();
            ds.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
            IdentitySchema.initialize(ds);
            var repository = new PreviewNodeRepository(new JdbcTemplate(ds));
            var connection =
                    new PreviewNodeConnection(
                            "test-a", "A", node.origin, node.pin, node.token, "127.0.0.1");
            repository.register(connection, node.service.snapshot().configVersion(), null);
            repository.mode("REMOTE", 0);
            var manager =
                    spy(
                            new PreviewNodeManager(
                                    repository,
                                    new PreviewNodeProperties(""),
                                    new PreviewProperties(64, 64),
                                    node.json));
            doAnswer(
                            invocation -> {
                                var client = spy(node.client());
                                doAnswer(
                                                request -> {
                                                    var receipt = request.callRealMethod();
                                                    if (((com.streamfusion.platform.media.preview
                                                                                    .control
                                                                                    .PreviewControl
                                                                                    .Command)
                                                                            request.getArgument(0))
                                                                    .action()
                                                            == com.streamfusion.platform.media
                                                                    .preview.control.PreviewControl
                                                                    .Action.CREATE)
                                                        throw new com.streamfusion.platform.media
                                                                .preview.PreviewFailure(
                                                                "NODE_UNREACHABLE");
                                                    return receipt;
                                                })
                                        .when(client)
                                        .apply(any(), any());
                                return client;
                            })
                    .when(manager)
                    .client(any());
            var executor = new RemotePreviewExecutor(repository, manager, node.json);
            String id = UUID.randomUUID().toString();
            executor.prepare(id);
            executor.attachSource(
                    id,
                    new SourceKey(1L, "v1"),
                    () -> URI.create("rtsp://192.0.2.10:554/live"),
                    () -> {},
                    budget());
            assertThat(executor.start(id, List.of("H264"), () -> {}, budget()).codec())
                    .isEqualTo("H264");
            verify(node.core, times(1)).prepare(id);
            assertThat(node.journal.snapshot().operations().values()).hasSize(1);
            executor.close(id, budget());
            assertThat(repository.viewers()).isEmpty();
        }
    }
}
