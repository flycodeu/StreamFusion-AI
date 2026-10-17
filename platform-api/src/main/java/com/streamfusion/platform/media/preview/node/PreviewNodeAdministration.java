package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import com.streamfusion.platform.system.access.mapper.AccessMapper;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import java.util.Map;
import java.util.function.Supplier;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/** Network preflight precedes short, authorization-locked configuration/audit transactions. */
@Service
public final class PreviewNodeAdministration {
    private final PreviewNodeManager manager;
    private final OpsPreviewCatalog ops;
    private final CurrentUserService current;
    private final AccessService access;
    private final AccessMapper coordination;
    private final AuditService audit;
    private final TransactionTemplate transactions;

    public PreviewNodeAdministration(
            PreviewNodeManager manager,
            OpsPreviewCatalog ops,
            CurrentUserService current,
            AccessService access,
            AccessMapper coordination,
            AuditService audit,
            PlatformTransactionManager transactions) {
        this.manager = manager;
        this.ops = ops;
        this.current = current;
        this.access = access;
        this.coordination = coordination;
        this.audit = audit;
        this.transactions = new TransactionTemplate(transactions);
    }

    public long reader() {
        long id = current.requireNormal().getId();
        if (!access.hasModuleAccess(id, "server"))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        return id;
    }

    public long administrator() {
        long id = reader();
        if (!access.isSuperAdmin(id)) throw BusinessException.error(ErrorCode.FORBIDDEN);
        return id;
    }

    public PreviewNodeManager.View register(AuditContextDto context) {
        return register((String) null, context);
    }

    public PreviewNodeManager.View register(String nodeId, AuditContextDto context) {
        administrator();
        var candidate = manager.registration(nodeId);
        return save("PREVIEW_NODE_REGISTER", () -> manager.register(candidate), context);
    }

    public PreviewNodeManager.View register(
            PreviewNodeManager.ConnectionInput input, AuditContextDto context) {
        administrator();
        var candidate = manager.registration(input);
        return save("PREVIEW_NODE_REGISTER", () -> manager.register(candidate), context);
    }

    public PreviewNodeManager.View importFromOps(
            OpsPreviewCatalog.Selection selection, AuditContextDto context) {
        administrator();
        var prepared = ops.prepare(selection, manager);
        try {
            var result =
                    save(
                            "PREVIEW_NODE_OPS_IMPORT",
                            () -> manager.register(prepared.registration()),
                            context);
            ops.discardPrevious(prepared, manager);
            return result;
        } catch (RuntimeException failure) {
            ops.discard(prepared.tokenFile());
            throw failure;
        }
    }

    public PreviewNodeManager.View mode(String mode, long version, AuditContextDto context) {
        administrator();
        var change = manager.checkMode(mode, version);
        return save("PREVIEW_NODE_MODE", () -> manager.changeMode(change), context);
    }

    public void policy(String nodeId, PreviewNodeManager.Policy policy, AuditContextDto context) {
        administrator();
        var checked = manager.checkPolicy(nodeId, policy);
        synchronized (manager) {
            transactions.executeWithoutResult(
                    status -> {
                        if (coordination.lockSuperAdminRole() == null)
                            throw BusinessException.error(ErrorCode.FORBIDDEN);
                        long actor = administrator();
                        manager.policy(checked);
                        audit.record(
                                actor,
                                "PREVIEW_NODE",
                                null,
                                "PREVIEW_NODE_POLICY",
                                "SUCCESS",
                                null,
                                context,
                                Map.of(
                                        "code",
                                        nodeId,
                                        "mode",
                                        policy.mode(),
                                        "version",
                                        Long.toString(policy.version() + 1)));
                    });
        }
    }

    private PreviewNodeManager.View save(
            String action, Supplier<PreviewNodeManager.View> mutation, AuditContextDto context) {
        synchronized (manager) {
            return transactions.execute(
                    status -> {
                        if (coordination.lockSuperAdminRole() == null)
                            throw BusinessException.error(ErrorCode.FORBIDDEN);
                        long actor = administrator();
                        var result = mutation.get();
                        audit.record(
                                actor,
                                "PREVIEW_NODE",
                                1L,
                                action,
                                "SUCCESS",
                                null,
                                context,
                                Map.of(
                                        "name",
                                        result.name(),
                                        "code",
                                        result.nodeId(),
                                        "mode",
                                        result.mode(),
                                        "version",
                                        Long.toString(result.version())));
                        return result;
                    });
        }
    }
}
