package com.streamfusion.platform.media.preview.node.zlm;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.control.PreviewBudget;
import com.streamfusion.platform.media.preview.node.OpsPreviewCatalog;
import com.streamfusion.platform.media.preview.node.PreviewNodeManager;
import com.streamfusion.platform.media.preview.node.PreviewNodeProperties;
import com.streamfusion.platform.media.preview.node.PreviewNodeRepository;
import com.streamfusion.platform.media.preview.node.PreviewScheduler;
import com.streamfusion.platform.media.preview.node.ZlmDirectConnection;
import com.streamfusion.platform.media.preview.node.ZlmDirectGateway;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import com.streamfusion.platform.system.access.mapper.AccessMapper;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.SecureRandom;
import java.time.Duration;
import java.time.Instant;
import java.util.HexFormat;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/** Owns registration and credentials for administrator-supplied, dedicated ZLM HTTP instances. */
@Service
public final class ZlmDirectService {
    public record Input(
            String name,
            String apiHost,
            int apiPort,
            String mediaAddress,
            String secret,
            Integer priority,
            PreviewBudget budget) {}

    public record Policy(String mode, int priority, PreviewBudget budget, long version) {}

    public record Credential(String secret, long version) {}

    public record Item(
            String nodeId,
            String name,
            String apiHost,
            int apiPort,
            String mediaAddress,
            String mode,
            long version,
            int priority,
            PreviewBudget budget,
            PreviewBudget.Usage usage,
            String state,
            String reasonCode,
            Instant observedAt,
            String zlmVersion) {}

    public record Listing(boolean registrationAvailable, List<Item> items) {}

    private final PreviewNodeRepository repository;
    private final PreviewNodeManager manager;
    private final PreviewScheduler scheduler;
    private final PreviewNodeProperties properties;
    private final ZlmDirectGateway gateway;
    private final CurrentUserService current;
    private final AccessService access;
    private final AccessMapper coordination;
    private final AuditService audit;
    private final OpsPreviewCatalog ops;
    private final TransactionTemplate transactions;
    private final SecureRandom random = new SecureRandom();

    public ZlmDirectService(
            PreviewNodeRepository repository,
            PreviewNodeManager manager,
            PreviewScheduler scheduler,
            PreviewNodeProperties properties,
            ZlmDirectGateway gateway,
            CurrentUserService current,
            AccessService access,
            AccessMapper coordination,
            AuditService audit,
            OpsPreviewCatalog ops,
            PlatformTransactionManager transactions) {
        this.repository = repository;
        this.manager = manager;
        this.scheduler = scheduler;
        this.properties = properties;
        this.gateway = gateway;
        this.current = current;
        this.access = access;
        this.coordination = coordination;
        this.audit = audit;
        this.ops = ops;
        this.transactions = new TransactionTemplate(transactions);
    }

    public Listing listing() {
        administrator();
        var states =
                manager.statuses(scheduler).stream()
                        .collect(
                                java.util.stream.Collectors.toMap(
                                        PreviewNodeManager.NodeStatus::nodeId, state -> state));
        var items =
                repository.nodes().stream()
                        .filter(node -> node.zlm() != null)
                        .map(node -> item(node, states.get(node.nodeId())))
                        .toList();
        return new Listing(manager.directRegistrationAvailable(), items);
    }

    public void create(Input input, AuditContextDto context) {
        create(input, context, "PREVIEW_ZLM_REGISTER");
    }

    public void importFromOps(OpsPreviewCatalog.Selection selection, AuditContextDto context) {
        administrator();
        var handedOff = ops.zlmHandoff(selection);
        var instance = handedOff.connection();
        URI api = URI.create(instance.apiUrl());
        create(
                new Input(
                        instance.name(),
                        api.getHost(),
                        api.getPort(),
                        instance.mediaAddress(),
                        handedOff.apiSecret(),
                        100,
                        new PreviewBudget(
                                instance.maxSources(),
                                instance.maxViewers(),
                                0,
                                4,
                                instance.maxSources() * 10240L,
                                instance.maxViewers() * 10240L,
                                8192,
                                8192)),
                context,
                "PREVIEW_ZLM_OPS_IMPORT");
    }

    private void create(Input input, AuditContextDto context, String action) {
        administrator();
        if (input == null) throw new PreviewFailure("ZLM_REGISTRATION_INVALID");
        String id = newNodeId();
        String secret = requireSecret(input.secret());
        int priority = input.priority() == null ? 100 : input.priority();
        PreviewBudget budget =
                input.budget() == null ? PreviewBudget.defaults(64, 0) : input.budget();
        requirePolicy(priority, budget);
        Path file = secretPath(id);
        var connection =
                connection(
                        id,
                        input.name(),
                        input.apiHost(),
                        input.apiPort(),
                        input.mediaAddress(),
                        file);
        if (repository.nodes().stream()
                .anyMatch(
                        node ->
                                node.zlm() != null
                                        && node.zlm().apiUri().equals(connection.apiUri())))
            throw new PreviewFailure("NODE_ALREADY_REGISTERED");
        OpsPreviewCatalog.writeProtectedSecret(file, secret);
        try {
            // Observe the candidate before committing it. A failed registration must never
            // leave a durable row behind while the API reports failure.
            var candidate =
                    new PreviewNodeRepository.Node(
                            null, "LOCAL", "candidate", 0, budget, priority, connection);
            manager.inspectDirect(candidate);
            String configVersion = revision();
            save(
                    action,
                    id,
                    connection.name(),
                    () -> {
                        if (repository.nodes().stream()
                                .anyMatch(
                                        node ->
                                                node.zlm() != null
                                                        && node.zlm()
                                                                .apiUri()
                                                                .equals(connection.apiUri())))
                            throw new PreviewFailure("NODE_ALREADY_REGISTERED");
                        repository.registerZlm(connection, budget, priority, configVersion);
                        return null;
                    },
                    context);
        } catch (RuntimeException failure) {
            manager.invalidate(id);
            if (repository.node(id) == null) discard(file);
            throw failure;
        }
    }

    public void policy(String id, Policy policy, AuditContextDto context) {
        administrator();
        var node = requireNode(id);
        if (policy == null
                || !java.util.Set.of("LOCAL", "REMOTE", "DRAINING").contains(policy.mode()))
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        requirePolicy(policy.priority(), policy.budget());
        if (node.version() != policy.version())
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        if ("REMOTE".equals(policy.mode())) requireReady(node.zlm());
        save(
                "PREVIEW_ZLM_POLICY",
                id,
                node.zlm().name(),
                () -> {
                    var latest = requireNode(id);
                    if (latest.version() != policy.version())
                        throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
                    if (repository.hasOwnership(id)
                            && (latest.budget().sourceUpperKbps()
                                            != policy.budget().sourceUpperKbps()
                                    || latest.budget().outputUpperKbps()
                                            != policy.budget().outputUpperKbps()))
                        throw new PreviewFailure("NODE_DRAIN_REQUIRED");
                    repository.zlmPolicy(
                            id,
                            policy.mode(),
                            policy.priority(),
                            policy.budget(),
                            null,
                            policy.version());
                    return null;
                },
                context);
    }

    public void credential(String id, Credential credential, AuditContextDto context) {
        administrator();
        var node = requireNode(id);
        if (credential == null || node.version() != credential.version())
            throw new PreviewFailure("NODE_CONFIGURATION_CHANGED");
        String secret = requireSecret(credential.secret());
        Path file = secretPath(id);
        var replacement =
                connection(
                        id,
                        node.zlm().name(),
                        node.zlm().apiUri().getHost(),
                        node.zlm().apiUri().getPort(),
                        node.zlm().mediaAddress(),
                        file);
        OpsPreviewCatalog.writeProtectedSecret(file, secret);
        try {
            requireReady(replacement);
            Path previous = node.zlm().secretFile();
            save(
                    "PREVIEW_ZLM_CREDENTIAL",
                    id,
                    node.zlm().name(),
                    () -> {
                        var latest = requireNode(id);
                        requireIdle(latest, credential.version());
                        repository.zlmCredential(id, file, revision(), credential.version());
                        return null;
                    },
                    context);
            manager.invalidate(id);
            discardOwned(previous);
        } catch (RuntimeException failure) {
            var latest = repository.node(id);
            if (latest == null || latest.zlm() == null || !file.equals(latest.zlm().secretFile()))
                discard(file);
            throw failure;
        }
    }

    public void remove(String id, long version, AuditContextDto context) {
        administrator();
        var node = requireNode(id);
        Path file = node.zlm().secretFile();
        save(
                "PREVIEW_ZLM_DELETE",
                id,
                node.zlm().name(),
                () -> {
                    requireIdle(requireNode(id), version);
                    repository.deleteZlm(id, version);
                    return null;
                },
                context);
        manager.invalidate(id);
        discardOwned(file);
    }

    private void requireIdle(PreviewNodeRepository.Node node, long version) {
        if (node.version() != version
                || "REMOTE".equals(node.mode())
                || repository.hasOwnership(node.nodeId()))
            throw new PreviewFailure("NODE_DRAIN_REQUIRED");
    }

    private void requireReady(ZlmDirectConnection connection) {
        var deadline = PreviewDeadline.after(Duration.ofSeconds(5));
        gateway.requireReady(connection, deadline);
        gateway.version(connection, deadline);
    }

    private PreviewNodeRepository.Node requireNode(String id) {
        var node = repository.node(id);
        if (node == null || node.zlm() == null) throw new PreviewFailure("NODE_NOT_REGISTERED");
        return node;
    }

    private Item item(PreviewNodeRepository.Node node, PreviewNodeManager.NodeStatus state) {
        var connection = node.zlm();
        return new Item(
                node.nodeId(),
                connection.name(),
                connection.apiUri().getHost(),
                connection.apiUri().getPort(),
                connection.mediaAddress(),
                node.mode(),
                node.version(),
                node.priority(),
                node.budget(),
                state.usage(),
                state.state(),
                state.reasonCode(),
                state.observedAt(),
                manager.directVersion(node.nodeId()));
    }

    private ZlmDirectConnection connection(
            String id, String name, String host, int port, String mediaAddress, Path file) {
        try {
            String media = mediaAddress == null || mediaAddress.isBlank() ? host : mediaAddress;
            return new ZlmDirectConnection(
                    id, name, URI.create("http://" + host + ":" + port), file, media);
        } catch (RuntimeException invalid) {
            throw new PreviewFailure("ZLM_REGISTRATION_INVALID");
        }
    }

    private void requirePolicy(int priority, PreviewBudget budget) {
        if (priority < 0 || priority > 10000 || budget == null || budget.maxTranscodes() != 0)
            throw new PreviewFailure("NODE_BUDGET_MISMATCH");
    }

    private static String requireSecret(String value) {
        if (value == null
                || value.isEmpty()
                || value.length() > 256
                || !value.equals(value.strip()))
            throw new PreviewFailure("ZLM_REGISTRATION_INVALID");
        for (int i = 0; i < value.length(); i++)
            if (value.charAt(i) < 32 || value.charAt(i) > 126)
                throw new PreviewFailure("ZLM_REGISTRATION_INVALID");
        return value;
    }

    private Path secretPath(String id) {
        try {
            if (!manager.directRegistrationAvailable()) throw new IllegalArgumentException();
            Path directory = Path.of(properties.credentialDirectory()).toRealPath();
            return directory.resolve("." + id + "-" + UUID.randomUUID() + ".secret");
        } catch (Exception invalid) {
            throw new PreviewFailure("NODE_CREDENTIAL_DIRECTORY_MISSING");
        }
    }

    private void discardOwned(Path path) {
        try {
            Path directory = Path.of(properties.credentialDirectory()).toRealPath();
            if (path.getParent().toRealPath().equals(directory)
                    && path.getFileName()
                            .toString()
                            .matches("\\.zlm-[a-f0-9]{16}-[a-f0-9-]{36}\\.secret"))
                Files.deleteIfExists(path);
        } catch (Exception ignored) {
            // A protected orphan can be removed by an administrator after audit.
        }
    }

    private static void discard(Path path) {
        try {
            Files.deleteIfExists(path);
        } catch (Exception ignored) {
        }
    }

    private String newNodeId() {
        byte[] bytes = new byte[8];
        random.nextBytes(bytes);
        return "zlm-" + HexFormat.of().formatHex(bytes);
    }

    private String revision() {
        byte[] bytes = new byte[32];
        random.nextBytes(bytes);
        return HexFormat.of().formatHex(bytes);
    }

    private <T> T save(
            String action, String id, String name, Supplier<T> mutation, AuditContextDto context) {
        synchronized (manager) {
            return transactions.execute(
                    status -> {
                        if (coordination.lockSuperAdminRole() == null)
                            throw BusinessException.error(ErrorCode.FORBIDDEN);
                        long actor = administrator();
                        T result = mutation.get();
                        audit.record(
                                actor,
                                "PREVIEW_NODE",
                                null,
                                action,
                                "SUCCESS",
                                null,
                                context,
                                Map.of("code", id, "name", name));
                        return result;
                    });
        }
    }

    private long administrator() {
        long id = current.requireNormal().getId();
        if (!access.hasModuleAccess(id, "server") || !access.isSuperAdmin(id))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        return id;
    }
}
