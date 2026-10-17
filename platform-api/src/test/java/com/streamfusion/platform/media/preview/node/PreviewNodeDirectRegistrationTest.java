package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.previewnode.PreviewControlTestFixture;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;

class PreviewNodeDirectRegistrationTest {
    @TempDir Path directory;

    @Test
    void registersWithRestrictedCredentialAndKeepsTheNodeOutOfScheduling() throws Exception {
        try (var node = new PreviewControlTestFixture(directory, "test-a")) {
            var secrets = Files.createDirectory(directory.resolve("platform-secrets"));
            Files.copy(node.token, secrets.resolve("test-a.token"));
            var repository = repository();
            var manager = manager(repository, secrets);
            var input = input(node, null);

            var registered = manager.register(manager.registration(input));
            assertThat(registered.mode()).isEqualTo("LOCAL");
            assertThat(repository.node("test-a").connection().tokenFile())
                    .isEqualTo(secrets.resolve("test-a.token"));
            assertThat(repository.node("test-a").mode()).isEqualTo("LOCAL");

            assertThatThrownBy(() -> manager.registration(input))
                    .isInstanceOf(PreviewFailure.class)
                    .hasMessage("NODE_CONFIGURATION_CHANGED");
        }
    }

    @Test
    void rejectsMissingCredentialAndPathShapedNodeIdBeforeNetworkAccess() throws Exception {
        var secrets = Files.createDirectory(directory.resolve("platform-secrets"));
        var manager = manager(repository(), secrets);
        assertThatThrownBy(
                        () ->
                                manager.registration(
                                        new PreviewNodeManager.ConnectionInput(
                                                "../outside",
                                                "bad",
                                                "https://127.0.0.1:9443",
                                                "a".repeat(64),
                                                "127.0.0.1",
                                                null)))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("NODE_REGISTRATION_INVALID");
        assertThatThrownBy(
                        () ->
                                manager.registration(
                                        new PreviewNodeManager.ConnectionInput(
                                                "missing",
                                                "Missing",
                                                "https://127.0.0.1:9443",
                                                "a".repeat(64),
                                                "127.0.0.1",
                                                null)))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("NODE_REGISTRATION_INVALID");
    }

    @Test
    void failedConnectionEditPreservesTheRegisteredNodeObservation() throws Exception {
        try (var node = new PreviewControlTestFixture(directory, "test-a")) {
            var secrets = Files.createDirectory(directory.resolve("platform-secrets"));
            Files.copy(node.token, secrets.resolve("test-a.token"));
            var repository = repository();
            var manager = manager(repository, secrets);
            var registered = manager.register(manager.registration(input(node, null)));
            manager.inspect(
                    repository.node("test-a"), PreviewDeadline.after(Duration.ofSeconds(4)));
            assertThat(manager.cached("test-a")).isNotNull();

            assertThatThrownBy(
                            () ->
                                    manager.registration(
                                            new PreviewNodeManager.ConnectionInput(
                                                    "test-a",
                                                    "changed",
                                                    "https://127.0.0.1:1",
                                                    node.pin,
                                                    "127.0.0.1",
                                                    registered.version())))
                    .isInstanceOf(PreviewFailure.class);
            assertThat(manager.cached("test-a")).isNotNull();
            assertThat(repository.node("test-a").connection().controlUri()).isEqualTo(node.origin);
        }
    }

    private PreviewNodeManager.ConnectionInput input(PreviewControlTestFixture node, Long version) {
        return new PreviewNodeManager.ConnectionInput(
                "test-a", "测试节点", node.origin.toString(), node.pin, "127.0.0.1", version);
    }

    private PreviewNodeManager manager(PreviewNodeRepository repository, Path secrets) {
        return new PreviewNodeManager(
                repository,
                new PreviewNodeProperties("", secrets.toString()),
                new PreviewProperties(64, 64),
                new ObjectMapper());
    }

    private PreviewNodeRepository repository() throws Exception {
        var dataSource = new JdbcDataSource();
        dataSource.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(dataSource);
        return new PreviewNodeRepository(new JdbcTemplate(dataSource));
    }
}
