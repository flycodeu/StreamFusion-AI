package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.control.PreviewControl;
import com.streamfusion.platform.media.preview.node.zlm.ZlmDirectService;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.system.access.mapper.AccessMapper;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicReference;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.DataSourceTransactionManager;

class PreviewOpsSynchronizationTest {
    @TempDir Path directory;
    PreviewNodeRepository nodes;
    JdbcTemplate sql;
    PreviewNodeManager manager = mock(PreviewNodeManager.class);
    PreviewNodeAdministration administration = mock(PreviewNodeAdministration.class);
    PreviewReservationLedger ledger = mock(PreviewReservationLedger.class);
    OpsPreviewCatalog ops = mock(OpsPreviewCatalog.class);
    ZlmDirectService zlm = mock(ZlmDirectService.class);
    ZlmDirectGateway gateway = mock(ZlmDirectGateway.class);
    AccessMapper access = mock(AccessMapper.class);
    AuditService audit = mock(AuditService.class);
    PreviewOpsSynchronization service;
    PreviewBudget budget = PreviewBudget.defaults(8, 1);
    AtomicReference<Path> lastFile = new AtomicReference<>();
    AtomicReference<String> release = new AtomicReference<>("release-b");
    AtomicReference<PreviewBudget> limit = new AtomicReference<>(PreviewBudget.defaults(64, 64));
    Path original;

    @BeforeEach
    void setup() throws Exception {
        var dataSource = new JdbcDataSource();
        dataSource.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(dataSource);
        sql = new JdbcTemplate(dataSource);
        nodes = new PreviewNodeRepository(sql);
        original = directory.resolve("original.token");
        Files.writeString(original, "a".repeat(64));
        nodes.register(connection(original, "Old name"), "config-a", null);
        nodes.policy("node-a", "DRAINING", 27, budget, 0);
        nodes.attachOpsSource("node-a", source("release-a"), 1);
        when(administration.administrator()).thenReturn(1L);
        when(access.lockSuperAdminRole()).thenReturn(1L);
        when(ledger.usage("node-a")).thenReturn(PreviewBudget.Usage.empty());
        when(ops.prepare(any(), any()))
                .thenAnswer(
                        invocation -> {
                            Path token =
                                    directory.resolve("candidate-" + UUID.randomUUID() + ".token");
                            Files.writeString(token, "a".repeat(64));
                            lastFile.set(token);
                            var snapshot =
                                    new PreviewControl.Snapshot(
                                            1,
                                            "node-a",
                                            "boot-a",
                                            "v1",
                                            "config-b",
                                            "MAINTENANCE",
                                            64,
                                            64,
                                            0,
                                            System.currentTimeMillis(),
                                            List.of(),
                                            limit.get(),
                                            PreviewBudget.Usage.empty(),
                                            null);
                            return new OpsPreviewCatalog.Prepared(
                                    new PreviewNodeManager.Registration(
                                            connection(token, "New name"),
                                            snapshot,
                                            nodes.node("node-a").version(),
                                            System.currentTimeMillis()),
                                    token,
                                    original,
                                    source(release.get()));
                        });
        doAnswer(
                        invocation -> {
                            Files.deleteIfExists(invocation.getArgument(0));
                            return null;
                        })
                .when(ops)
                .discard(any());
        doAnswer(
                        invocation ->
                                nodes.nodes().stream()
                                        .anyMatch(
                                                n ->
                                                        n.connection() != null
                                                                && n.connection()
                                                                        .tokenFile()
                                                                        .equals(
                                                                                invocation
                                                                                        .getArgument(
                                                                                                0))))
                .when(manager)
                .credentialInUse(any());
        service =
                new PreviewOpsSynchronization(
                        nodes,
                        ledger,
                        manager,
                        administration,
                        ops,
                        zlm,
                        gateway,
                        access,
                        audit,
                        new DataSourceTransactionManager(dataSource));
    }

    @Test
    void reviewIsReadOnlyAndSyncPreservesIdentityBudgetPriorityAndDrain() throws Exception {
        var before = nodes.node("node-a");
        var review = service.inspect("node-a", input(null));
        assertThat(nodes.node("node-a")).isEqualTo(before);
        assertThat(lastFile.get()).doesNotExist();
        assertThat(review.retainedBudget()).isEqualTo(budget);
        var result = service.apply("node-a", input(review.digest()), context());
        var after = nodes.node("node-a");
        assertThat(after.nodeId()).isEqualTo(before.nodeId());
        assertThat(after.mode()).isEqualTo("DRAINING");
        assertThat(after.priority()).isEqualTo(27);
        assertThat(after.budget()).isEqualTo(budget);
        assertThat(after.opsSource().releaseId()).isEqualTo("release-b");
        assertThat(after.opsSource().syncedAt()).isNotNull();
        assertThat(after.configVersion()).isEqualTo("config-b");
        assertThat(after.version()).isEqualTo(before.version() + 2);
        assertThat(lastFile.get()).exists();
        assertThat(result.version()).isEqualTo(after.version());
        assertThat(Files.readString(original)).isEqualTo("a".repeat(64));
        verify(ops).discardPrevious(any(), eq(manager));
        verify(manager).invalidate("node-a");
    }

    @Test
    void applyingSameCurrentCandidateDoesNotChurnVersionOrCredential() {
        var review = service.inspect("node-a", input(null));
        service.apply("node-a", input(review.digest()), context());
        var after = nodes.node("node-a");
        var next =
                new PreviewOpsSynchronization.Input(
                        "controller-a",
                        "target-a",
                        "instance-a",
                        "release-b",
                        after.version(),
                        null);
        var repeated = service.inspect("node-a", next);
        service.apply(
                "node-a",
                new PreviewOpsSynchronization.Input(
                        next.controllerId(),
                        next.targetId(),
                        next.instanceId(),
                        next.releaseId(),
                        next.version(),
                        repeated.digest()),
                context());
        assertThat(nodes.node("node-a")).isEqualTo(after);
        assertThat(lastFile.get()).doesNotExist();
    }

    @Test
    void pendingOperationAndEveryResourceResponsibilityBlockBeforeHandoff() {
        when(ledger.hasUnresolvedOperations("node-a")).thenReturn(true);
        assertFailure(() -> service.inspect("node-a", input(null)), "NODE_DRAIN_REQUIRED");
        when(ledger.hasUnresolvedOperations("node-a")).thenReturn(false);
        for (var usage :
                List.of(
                        new PreviewBudget.Usage(1, 0, 0, 0, 0, 0),
                        new PreviewBudget.Usage(0, 1, 0, 0, 0, 0),
                        new PreviewBudget.Usage(0, 0, 1, 0, 0, 0),
                        new PreviewBudget.Usage(0, 0, 0, 1, 0, 0),
                        new PreviewBudget.Usage(0, 0, 0, 0, 1, 0),
                        new PreviewBudget.Usage(0, 0, 0, 0, 0, 1))) {
            when(ledger.usage("node-a")).thenReturn(usage);
            assertFailure(() -> service.inspect("node-a", input(null)), "NODE_DRAIN_REQUIRED");
        }
        verifyNoInteractions(ops);
    }

    @Test
    void rejectsRebindingStaleVersionsReleaseChangeAndNoReview() {
        assertFailure(
                () -> service.apply("node-a", input(null), context()), "OPS_SYNC_REVIEW_REQUIRED");
        assertFailure(
                () ->
                        service.inspect(
                                "node-a",
                                new PreviewOpsSynchronization.Input(
                                        "controller-a",
                                        "target-other",
                                        "instance-a",
                                        "release-b",
                                        2,
                                        null)),
                "OPS_INSTANCE_MISMATCH");
        var review = service.inspect("node-a", input(null));
        release.set("release-c");
        assertFailure(
                () -> service.apply("node-a", input(review.digest()), context()),
                "OPS_INSTANCE_MISMATCH");
        assertThat(lastFile.get()).doesNotExist();
        release.set("release-b");
        nodes.policy("node-a", "DRAINING", 28, budget, 2);
        assertFailure(
                () -> service.apply("node-a", input(review.digest()), context()),
                "NODE_CONFIGURATION_CHANGED");
    }

    @Test
    void budgetReductionAndChangedBitrateBasisNeverOverwritePlatformCalibration() {
        limit.set(PreviewBudget.defaults(4, 0));
        assertFailure(() -> service.inspect("node-a", input(null)), "NODE_BUDGET_MISMATCH");
        assertThat(lastFile.get()).doesNotExist();
        assertThat(nodes.node("node-a").budget()).isEqualTo(budget);
        assertFailure(
                () ->
                        PreviewOpsSynchronization.requireBudget(
                                budget,
                                new PreviewBudget(64, 64, 64, 4, 655360, 655360, 9000, 9000)),
                "NODE_BUDGET_MISMATCH");
    }

    @Test
    void authorizationIsRecheckedAfterNetworkAndFailedCommitPreservesOldSecretReference() {
        var review = service.inspect("node-a", input(null));
        var before = nodes.node("node-a");
        when(administration.administrator())
                .thenReturn(1L)
                .thenThrow(BusinessException.error(ErrorCode.FORBIDDEN));
        assertThatThrownBy(() -> service.apply("node-a", input(review.digest()), context()))
                .isInstanceOf(BusinessException.class);
        assertThat(nodes.node("node-a")).isEqualTo(before);
        assertThat(lastFile.get()).doesNotExist();
        assertThat(original).exists();
    }

    @Test
    void explicitLegacyBindingKeepsExistingNodeRatherThanCreatingAnother() {
        sql.update(
                "UPDATE preview_node SET ops_controller_id=NULL,ops_target_id=NULL,ops_instance_id=NULL,ops_release_id=NULL,ops_synced_at=NULL WHERE node_id='node-a'");
        int count = nodes.nodes().size();
        var review = service.inspect("node-a", input(null));
        assertThat(nodes.node("node-a").opsSource()).isNull();
        service.apply("node-a", input(review.digest()), context());
        assertThat(nodes.nodes()).hasSize(count);
        assertThat(nodes.node("node-a").opsSource().sameInstance(source("release-b"))).isTrue();
    }

    @Test
    void zlmSyncKeepsLegacyNodeIdAndUsesFrozenReleaseCredentialAfterPhysicalDrain()
            throws Exception {
        var input = zlmFixture(new AtomicReference<>("credential-a:2"));
        var review = service.inspect("zlm-1234567890abcdef", input);
        assertThat(review.credentialChanged()).isTrue();
        assertThat(lastFile.get()).doesNotExist();
        service.apply(
                "zlm-1234567890abcdef",
                new PreviewOpsSynchronization.Input(
                        input.controllerId(),
                        input.targetId(),
                        input.instanceId(),
                        input.releaseId(),
                        input.version(),
                        review.digest()),
                context());
        var applied = nodes.node("zlm-1234567890abcdef");
        assertThat(applied.nodeId()).isEqualTo("zlm-1234567890abcdef");
        assertThat(applied.opsSource().sameInstance(source("release-b"))).isTrue();
        assertThat(applied.mode()).isEqualTo("DRAINING");
        assertThat(applied.priority()).isEqualTo(23);
        assertThat(applied.budget()).isEqualTo(PreviewBudget.defaults(8, 0));
        assertThat(nodes.zlmInstallPath(applied.nodeId()))
                .isEqualTo("/opt/zlm/instances/instance-a/releases/release-b");
        assertThat(Files.readString(applied.zlm().secretFile())).isEqualTo("new-secret");
        assertThat(directory.resolve("old-zlm.secret")).doesNotExist();
        verify(gateway, times(3)).requireIdle(any(), any());
    }

    @Test
    void zlmPhysicalActivityBeforeCommitBlocksSynchronization() throws Exception {
        var input = zlmFixture(new AtomicReference<>("credential-a:2"));
        var review = service.inspect("zlm-1234567890abcdef", input);
        var before = nodes.node("zlm-1234567890abcdef");
        doNothing()
                .doThrow(new PreviewFailure("NODE_RESOURCES_ACTIVE"))
                .when(gateway)
                .requireIdle(any(), any());
        assertFailure(
                () ->
                        service.apply(
                                before.nodeId(),
                                new PreviewOpsSynchronization.Input(
                                        input.controllerId(),
                                        input.targetId(),
                                        input.instanceId(),
                                        input.releaseId(),
                                        input.version(),
                                        review.digest()),
                                context()),
                "NODE_RESOURCES_ACTIVE");
        assertThat(nodes.node(before.nodeId())).isEqualTo(before);
        assertThat(lastFile.get()).doesNotExist();
        assertThat(before.zlm().secretFile()).exists();
        verifyNoInteractions(audit);
    }

    @Test
    void zlmCredentialRevisionChangeBetweenReviewAndApplyRequiresFreshReview() throws Exception {
        var revision = new AtomicReference<>("credential-a:2");
        var input = zlmFixture(revision);
        var before = nodes.node("zlm-1234567890abcdef");
        var review = service.inspect(before.nodeId(), input);
        revision.set("credential-a:3");
        assertFailure(
                () ->
                        service.apply(
                                before.nodeId(),
                                new PreviewOpsSynchronization.Input(
                                        input.controllerId(),
                                        input.targetId(),
                                        input.instanceId(),
                                        input.releaseId(),
                                        input.version(),
                                        review.digest()),
                                context()),
                "OPS_SYNC_REVIEW_CHANGED");
        assertThat(nodes.node(before.nodeId())).isEqualTo(before);
        assertThat(lastFile.get()).doesNotExist();
        assertThat(before.zlm().secretFile()).exists();
        verifyNoInteractions(audit);
    }

    private PreviewOpsSynchronization.Input zlmFixture(AtomicReference<String> revision)
            throws Exception {
        sql.update("DELETE FROM preview_node WHERE node_id='node-a'");
        Path oldSecret = directory.resolve("old-zlm.secret");
        Files.writeString(oldSecret, "old-secret");
        var connection =
                new ZlmDirectConnection(
                        "zlm-1234567890abcdef",
                        "ZLM A",
                        URI.create("http://127.0.0.1:18080"),
                        oldSecret,
                        "127.0.0.1");
        nodes.registerZlm(connection, PreviewBudget.defaults(8, 0), 23, "config-zlm-a");
        nodes.zlmPolicy(connection.nodeId(), "DRAINING", 23, PreviewBudget.defaults(8, 0), null, 0);
        when(ledger.usage(connection.nodeId())).thenReturn(PreviewBudget.Usage.empty());
        when(ops.zlmHandoff(any()))
                .thenAnswer(
                        invocation ->
                                new OpsPreviewCatalog.ZlmHandoff(
                                        new OpsPreviewCatalog.ZlmInstance(
                                                "instance-a",
                                                "ZLM B",
                                                "Target A",
                                                "127.0.0.1",
                                                "/opt/zlm",
                                                "/opt/zlm/instances/instance-a/releases/release-b",
                                                "release-b",
                                                "http://127.0.0.1:18080",
                                                "127.0.0.1",
                                                18080,
                                                18000,
                                                64,
                                                64,
                                                0,
                                                "controller-a",
                                                "target-a",
                                                revision.get()),
                                        "new-secret"));
        when(ops.source(any(OpsPreviewCatalog.ZlmInstance.class))).thenReturn(source("release-b"));
        when(zlm.prepareOpsConnection(eq(connection.nodeId()), any()))
                .thenAnswer(
                        invocation -> {
                            Path file =
                                    directory.resolve("new-zlm-" + UUID.randomUUID() + ".secret");
                            Files.writeString(file, "new-secret");
                            lastFile.set(file);
                            return new ZlmDirectConnection(
                                    connection.nodeId(),
                                    "ZLM B",
                                    connection.apiUri(),
                                    file,
                                    connection.mediaAddress());
                        });
        doAnswer(
                        invocation -> {
                            Path file = invocation.getArgument(0);
                            if (nodes.nodes().stream()
                                    .noneMatch(
                                            n ->
                                                    n.zlm() != null
                                                            && n.zlm().secretFile().equals(file)))
                                Files.deleteIfExists(file);
                            return null;
                        })
                .when(zlm)
                .discardOpsCredential(any());
        return new PreviewOpsSynchronization.Input(
                "controller-a", "target-a", "instance-a", "release-b", 1, null);
    }

    private PreviewNodeConnection connection(Path file, String name) {
        return new PreviewNodeConnection(
                "node-a",
                name,
                URI.create("https://127.0.0.1:18443"),
                "a".repeat(64),
                file,
                "127.0.0.1");
    }

    private OpsNodeSource source(String releaseId) {
        return new OpsNodeSource("controller-a", "target-a", "instance-a", releaseId, null);
    }

    private PreviewOpsSynchronization.Input input(String digest) {
        return new PreviewOpsSynchronization.Input(
                "controller-a", "target-a", "instance-a", "release-b", 2, digest);
    }

    private AuditContextDto context() {
        return new AuditContextDto(null, null);
    }

    private void assertFailure(Runnable operation, String code) {
        assertThatThrownBy(operation::run).isInstanceOf(PreviewFailure.class).hasMessage(code);
    }
}
