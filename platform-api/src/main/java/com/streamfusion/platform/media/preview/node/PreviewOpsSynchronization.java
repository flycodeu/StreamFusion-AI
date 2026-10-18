package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.node.zlm.ZlmDirectService;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import com.streamfusion.platform.system.access.mapper.AccessMapper;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.Duration;
import java.time.Instant;
import java.util.HexFormat;
import java.util.Map;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/** Explicit provenance binding and in-place post-maintenance sync; never enables scheduling. */
@Service
public final class PreviewOpsSynchronization {
    public record Input(
            String controllerId,
            String targetId,
            String instanceId,
            String releaseId,
            long version,
            String digest) {}

    public record Review(
            String nodeId,
            long version,
            OpsNodeSource previousSource,
            OpsNodeSource candidateSource,
            String previousConfigVersion,
            String candidateConfigVersion,
            String previousName,
            String candidateName,
            String previousInstallPath,
            String candidateInstallPath,
            PreviewBudget retainedBudget,
            PreviewBudget candidateLimit,
            boolean credentialChanged,
            String digest,
            Instant checkedAt) {}

    private record Candidate(
            OpsNodeSource source,
            String configVersion,
            String name,
            PreviewBudget limit,
            boolean credentialChanged,
            OpsPreviewCatalog.Prepared managed,
            ZlmDirectConnection zlm,
            String installPath) {}

    private final PreviewNodeRepository nodes;
    private final PreviewReservationLedger ledger;
    private final PreviewNodeManager manager;
    private final PreviewNodeAdministration administration;
    private final OpsPreviewCatalog ops;
    private final ZlmDirectService zlm;
    private final ZlmDirectGateway gateway;
    private final AccessMapper coordination;
    private final AuditService audit;
    private final TransactionTemplate transactions;

    public PreviewOpsSynchronization(
            PreviewNodeRepository nodes,
            PreviewReservationLedger ledger,
            PreviewNodeManager manager,
            PreviewNodeAdministration administration,
            OpsPreviewCatalog ops,
            ZlmDirectService zlm,
            ZlmDirectGateway gateway,
            AccessMapper coordination,
            AuditService audit,
            PlatformTransactionManager transactions) {
        this.nodes = nodes;
        this.ledger = ledger;
        this.manager = manager;
        this.administration = administration;
        this.ops = ops;
        this.zlm = zlm;
        this.gateway = gateway;
        this.coordination = coordination;
        this.audit = audit;
        this.transactions = new TransactionTemplate(transactions);
    }

    public Review inspect(String id, Input input) {
        administration.administrator();
        var node = requireIdle(id, input);
        var candidate = prepare(node, input);
        try {
            return review(node, candidate);
        } finally {
            discard(candidate);
        }
    }

    public Review apply(String id, Input input, AuditContextDto context) {
        administration.administrator();
        var node = requireIdle(id, input);
        if (input.digest() == null || !input.digest().matches("[a-f0-9]{64}"))
            throw new PreviewFailure("OPS_SYNC_REVIEW_REQUIRED");
        // Handoff, TLS/API readiness and physical emptiness checks never hold a SQL transaction.
        var candidate = prepare(node, input);
        try {
            var review = review(node, candidate);
            if (!review.digest().equals(input.digest()))
                throw new PreviewFailure("OPS_SYNC_REVIEW_CHANGED");
            // Keep the external observation close to the commit. This is still a point-in-time
            // check; without a ZLM-side maintenance seal, external publishers can race it.
            if (candidate.zlm() != null)
                gateway.requireIdle(candidate.zlm(), PreviewDeadline.after(Duration.ofSeconds(4)));
            synchronized (manager) {
                transactions.executeWithoutResult(
                        status -> {
                            if (coordination.lockSuperAdminRole() == null)
                                throw BusinessException.error(ErrorCode.FORBIDDEN);
                            long actor = administration.administrator();
                            var current = requireIdle(id, input);
                            requireIdentity(current, candidate.source());
                            if (current.opsSource() != null
                                    && current.opsSource()
                                            .releaseId()
                                            .equals(candidate.source().releaseId())
                                    && current.configVersion().equals(candidate.configVersion())
                                    && review.previousName().equals(candidate.name())
                                    && java.util.Objects.equals(
                                            review.previousInstallPath(), candidate.installPath())
                                    && !candidate.credentialChanged())
                                return; // Repeated application has no additional durable effect.
                            if (candidate.managed() != null) {
                                if (System.currentTimeMillis()
                                                - candidate.managed().registration().checkedAt()
                                        > 5000) throw new PreviewFailure("OPS_SYNC_REVIEW_CHANGED");
                                nodes.synchronizeManaged(
                                        id,
                                        candidate.managed().registration().connection(),
                                        candidate.configVersion(),
                                        candidate.source(),
                                        input.version());
                            } else {
                                if (review.checkedAt().isBefore(Instant.now().minusSeconds(5)))
                                    throw new PreviewFailure("OPS_SYNC_REVIEW_CHANGED");
                                nodes.synchronizeZlm(
                                        id,
                                        candidate.zlm(),
                                        candidate.configVersion(),
                                        candidate.source(),
                                        candidate.installPath(),
                                        input.version());
                            }
                            audit.record(
                                    actor,
                                    "PREVIEW_NODE",
                                    null,
                                    "PREVIEW_NODE_OPS_SYNC",
                                    "SUCCESS",
                                    null,
                                    context,
                                    Map.of(
                                            "code",
                                            id,
                                            "name",
                                            candidate.name(),
                                            "mode",
                                            "DRAINING",
                                            "version",
                                            Long.toString(input.version() + 2)));
                        });
                manager.invalidate(id);
            }
            if (candidate.managed() != null) ops.discardPrevious(candidate.managed(), manager);
            else zlm.discardOpsCredential(node.zlm().secretFile());
            return review(nodes.node(id), candidate);
        } finally {
            // A successful CAS now references the new secret, so only unreferenced files are
            // removed.
            discard(candidate);
        }
    }

    private PreviewNodeRepository.Node requireIdle(String id, Input input) {
        var node = nodes.node(id);
        if (node == null || node.local()) throw new PreviewFailure("REMOTE_NODE_REQUIRED");
        if (input == null || node.version() != input.version())
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        if (!"DRAINING".equals(node.mode())) throw new PreviewFailure("NODE_DRAIN_REQUIRED");
        var usage = ledger.usage(id);
        if (nodes.hasOwnership(id)
                || ledger.hasUnresolvedOperations(id)
                || usage == null
                || !PreviewBudget.Usage.empty().equals(usage))
            throw new PreviewFailure("NODE_DRAIN_REQUIRED");
        var requested =
                new OpsNodeSource(
                        input.controllerId(),
                        input.targetId(),
                        input.instanceId(),
                        input.releaseId(),
                        null);
        requireIdentity(node, requested);
        return node;
    }

    private void requireIdentity(PreviewNodeRepository.Node node, OpsNodeSource source) {
        if (source == null) throw new PreviewFailure("OPS_IDENTITY_REQUIRED");
        if (node.opsSource() != null && !node.opsSource().sameInstance(source))
            throw new PreviewFailure("OPS_INSTANCE_MISMATCH");
        if (nodes.nodes().stream()
                .anyMatch(
                        other ->
                                !other.nodeId().equals(node.nodeId())
                                        && source.sameInstance(other.opsSource())))
            throw new PreviewFailure("OPS_INSTANCE_ALREADY_LINKED");
    }

    private Candidate prepare(PreviewNodeRepository.Node node, Input input) {
        var selection = new OpsPreviewCatalog.Selection(input.instanceId(), input.releaseId());
        if (node.zlm() == null) {
            var prepared = ops.prepare(selection, manager);
            try {
                requireSelected(node, input, prepared.source());
                var candidate = prepared.registration().connection();
                if (!node.nodeId().equals(candidate.nodeId())
                        || !node.connection().controlUri().equals(candidate.controlUri())
                        || !node.connection().mediaAddress().equals(candidate.mediaAddress())
                        || !node.connection()
                                .certificateSha256()
                                .equals(candidate.certificateSha256())
                        || !sameSecret(node.connection().tokenFile(), candidate.tokenFile()))
                    throw new PreviewFailure("OPS_CONNECTION_CHANGED");
                var snapshot = prepared.registration().snapshot();
                if (snapshot.usage() != null
                        && !PreviewBudget.Usage.empty().equals(snapshot.usage()))
                    throw new PreviewFailure("NODE_RESOURCES_ACTIVE");
                requireBudget(node.budget(), snapshot.budget());
                return new Candidate(
                        prepared.source(),
                        snapshot.configVersion(),
                        candidate.name(),
                        snapshot.budget(),
                        false,
                        prepared,
                        null,
                        null);
            } catch (RuntimeException failure) {
                ops.discard(prepared.tokenFile());
                throw failure;
            }
        }
        var handoff = ops.zlmHandoff(selection);
        var source = ops.source(handoff.connection());
        requireSelected(node, input, source);
        var published = handoff.connection();
        if (published.credentialRevision() == null)
            throw new PreviewFailure("OPS_IDENTITY_REQUIRED");
        if (!node.zlm().apiUri().toString().equals(published.apiUrl())
                || !node.zlm().mediaAddress().equals(published.mediaAddress()))
            throw new PreviewFailure("OPS_CONNECTION_CHANGED");
        var limit =
                new PreviewBudget(
                        published.maxSources(),
                        published.maxViewers(),
                        0,
                        4,
                        published.maxSources() * 10240L,
                        published.maxViewers() * 10240L,
                        8192,
                        8192);
        requireBudget(node.budget(), limit);
        var connection = zlm.prepareOpsConnection(node.nodeId(), handoff);
        try {
            boolean credentialChanged =
                    !sameSecret(node.zlm().secretFile(), connection.secretFile());
            gateway.requireIdle(connection, PreviewDeadline.after(Duration.ofSeconds(4)));
            String configVersion =
                    hash(
                            source.controllerId()
                                    + "|"
                                    + source.targetId()
                                    + "|"
                                    + source.instanceId()
                                    + "|"
                                    + source.releaseId()
                                    + "|"
                                    + published.apiUrl()
                                    + "|"
                                    + published.mediaAddress()
                                    + "|"
                                    + published.installPath()
                                    + "|"
                                    + published.credentialRevision());
            return new Candidate(
                    source,
                    configVersion,
                    connection.name(),
                    limit,
                    credentialChanged,
                    null,
                    connection,
                    published.installPath());
        } catch (RuntimeException failure) {
            zlm.discardOpsCredential(connection.secretFile());
            throw failure;
        }
    }

    private void requireSelected(
            PreviewNodeRepository.Node node, Input input, OpsNodeSource source) {
        requireIdentity(node, source);
        if (!input.controllerId().equals(source.controllerId())
                || !input.targetId().equals(source.targetId())
                || !input.instanceId().equals(source.instanceId())
                || !input.releaseId().equals(source.releaseId()))
            throw new PreviewFailure("OPS_INSTANCE_MISMATCH");
    }

    static void requireBudget(PreviewBudget retained, PreviewBudget limit) {
        if (limit == null
                || retained.maxSources() > limit.maxSources()
                || retained.maxViewers() > limit.maxViewers()
                || retained.maxTranscodes() > limit.maxTranscodes()
                || retained.maxStarting() > limit.maxStarting()
                || retained.ingressKbps() > limit.ingressKbps()
                || retained.egressKbps() > limit.egressKbps()
                || retained.sourceUpperKbps() != limit.sourceUpperKbps()
                || retained.outputUpperKbps() != limit.outputUpperKbps())
            throw new PreviewFailure("NODE_BUDGET_MISMATCH");
    }

    private Review review(PreviewNodeRepository.Node node, Candidate candidate) {
        String name = node.zlm() == null ? node.connection().name() : node.zlm().name();
        String digest =
                hash(
                        node.nodeId()
                                + "|"
                                + node.version()
                                + "|"
                                + candidate.source()
                                + "|"
                                + candidate.configVersion()
                                + "|"
                                + candidate.name()
                                + "|"
                                + candidate.installPath()
                                + "|"
                                + node.budget()
                                + "|"
                                + candidate.limit()
                                + "|"
                                + candidate.credentialChanged());
        return new Review(
                node.nodeId(),
                node.version(),
                node.opsSource(),
                candidate.source(),
                node.configVersion(),
                candidate.configVersion(),
                name,
                candidate.name(),
                node.zlm() == null ? null : nodes.zlmInstallPath(node.nodeId()),
                candidate.installPath(),
                node.budget(),
                candidate.limit(),
                candidate.credentialChanged(),
                digest,
                Instant.now());
    }

    private void discard(Candidate candidate) {
        if (candidate.managed() != null) {
            synchronized (manager) {
                if (!manager.credentialInUse(candidate.managed().tokenFile()))
                    ops.discard(candidate.managed().tokenFile());
            }
        } else zlm.discardOpsCredential(candidate.zlm().secretFile());
    }

    private static boolean sameSecret(Path a, Path b) {
        try {
            if (Files.size(a) > 512 || Files.size(b) > 512) return false;
            return MessageDigest.isEqual(
                    Files.readString(a).strip().getBytes(StandardCharsets.US_ASCII),
                    Files.readString(b).strip().getBytes(StandardCharsets.US_ASCII));
        } catch (Exception invalid) {
            throw new PreviewFailure("NODE_TOKEN_INVALID");
        }
    }

    private static String hash(String value) {
        try {
            return HexFormat.of()
                    .formatHex(
                            MessageDigest.getInstance("SHA-256")
                                    .digest(value.getBytes(StandardCharsets.UTF_8)));
        } catch (java.security.NoSuchAlgorithmException unavailable) {
            throw new IllegalStateException(unavailable);
        }
    }
}
