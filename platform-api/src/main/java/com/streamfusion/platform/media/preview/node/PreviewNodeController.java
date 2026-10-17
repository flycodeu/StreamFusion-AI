package com.streamfusion.platform.media.preview.node;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.node.zlm.ZlmDirectService;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import jakarta.servlet.http.HttpServletRequest;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

/** Minimal management surface under the existing service-information permission. */
@RestController
@RequestMapping("/preview-node")
@ModuleAccess("server")
@RequiredArgsConstructor
public class PreviewNodeController {
    private final PreviewNodeManager manager;
    private final RemotePreviewExecutor executor;
    private final CurrentUserService current;
    private final AccessService access;
    private final AuditService audit;
    private final PreviewNodeAdministration administration;
    private final OpsPreviewCatalog ops;
    private final ZlmDirectService zlm;
    private final PreviewScheduler scheduler;
    private final PreviewNodeMaintenanceService maintenance;
    private final com.streamfusion.platform.media.preview.execution.PreviewRoutingExecutor routing;

    @GetMapping
    public R<PreviewNodeManager.View> get() {
        reader();
        return R.success(manager.view());
    }

    @PostMapping("/check")
    public R<PreviewNodeManager.View> check() {
        reader();
        return R.success(manager.check());
    }

    @PostMapping("/register")
    public R<ActionResult> register(
            @RequestParam(required = false) String nodeId, HttpServletRequest request) {
        administrator();
        return action(() -> administration.register(nodeId, AuditContextDto.from(request)));
    }

    @PostMapping("/connections")
    public R<ActionResult> connection(
            @RequestBody PreviewNodeManager.ConnectionInput input, HttpServletRequest request) {
        administrator();
        return action(() -> administration.register(input, AuditContextDto.from(request)));
    }

    @GetMapping("/ops-instances")
    public R<OpsPreviewCatalog.Listing> opsInstances() {
        administrator();
        return R.success(ops.listing());
    }

    @PostMapping("/ops-import")
    public R<ActionResult> opsImport(
            @RequestBody OpsPreviewCatalog.Selection selection, HttpServletRequest request) {
        administrator();
        return action(() -> administration.importFromOps(selection, AuditContextDto.from(request)));
    }

    @GetMapping("/zlm-instances")
    public R<ZlmDirectService.Listing> zlmInstances() {
        administrator();
        return R.success(zlm.listing());
    }

    @GetMapping("/ops-zlm-instances")
    public R<OpsPreviewCatalog.ZlmListing> opsZlmInstances() {
        administrator();
        return R.success(ops.zlmListing());
    }

    @PostMapping("/ops-zlm-import")
    public R<PolicyResult> opsZlmImport(
            @RequestBody OpsPreviewCatalog.Selection selection, HttpServletRequest request) {
        administrator();
        return zlmAction(() -> zlm.importFromOps(selection, AuditContextDto.from(request)));
    }

    @PostMapping("/zlm-instances")
    public R<PolicyResult> zlmCreate(
            @RequestBody ZlmDirectService.Input input, HttpServletRequest request) {
        administrator();
        return zlmAction(() -> zlm.create(input, AuditContextDto.from(request)));
    }

    @PutMapping("/zlm-instances/{nodeId}")
    public R<PolicyResult> zlmPolicy(
            @PathVariable String nodeId,
            @RequestBody ZlmDirectService.Policy input,
            HttpServletRequest request) {
        administrator();
        return zlmAction(() -> zlm.policy(nodeId, input, AuditContextDto.from(request)));
    }

    @PutMapping("/zlm-instances/{nodeId}/credential")
    public R<PolicyResult> zlmCredential(
            @PathVariable String nodeId,
            @RequestBody ZlmDirectService.Credential input,
            HttpServletRequest request) {
        administrator();
        return zlmAction(() -> zlm.credential(nodeId, input, AuditContextDto.from(request)));
    }

    @DeleteMapping("/zlm-instances/{nodeId}")
    public R<PolicyResult> zlmRemove(
            @PathVariable String nodeId, @RequestParam long version, HttpServletRequest request) {
        administrator();
        return zlmAction(() -> zlm.remove(nodeId, version, AuditContextDto.from(request)));
    }

    public record Cluster(
            java.util.List<PreviewNodeManager.NodeStatus> nodes,
            java.util.List<PreviewNodeManager.RegistrationOption> registrationOptions,
            boolean directRegistrationAvailable) {}

    @GetMapping("/nodes")
    public R<Cluster> nodes() {
        reader();
        return R.success(
                new Cluster(
                        manager.statuses(scheduler),
                        access.isSuperAdmin(current.requireNormal().getId())
                                ? manager.registrationOptions()
                                : java.util.List.of(),
                        access.isSuperAdmin(current.requireNormal().getId())
                                && manager.directRegistrationAvailable()));
    }

    public record PolicyResult(boolean applied, String reasonCode) {}

    private R<PolicyResult> zlmAction(Runnable operation) {
        try {
            operation.run();
            return R.success(new PolicyResult(true, null));
        } catch (PreviewFailure failure) {
            return R.success(new PolicyResult(false, failure.reason()));
        }
    }

    @GetMapping("/nodes/{nodeId}/maintenance")
    public R<MaintenanceResult> maintenance(@PathVariable String nodeId) {
        reader();
        try {
            return R.success(new MaintenanceResult(true, null, maintenance.inspect(nodeId)));
        } catch (PreviewFailure failure) {
            return R.success(new MaintenanceResult(false, failure.reason(), null));
        }
    }

    @PostMapping("/nodes/{nodeId}/maintenance")
    public R<MaintenanceResult> maintain(
            @PathVariable String nodeId,
            @RequestBody PreviewNodeMaintenanceService.Input input,
            HttpServletRequest request) {
        administrator();
        try {
            return R.success(
                    new MaintenanceResult(
                            true,
                            null,
                            maintenance.apply(nodeId, input, AuditContextDto.from(request))));
        } catch (PreviewFailure failure) {
            return R.success(new MaintenanceResult(false, failure.reason(), null));
        }
    }

    public record MaintenanceResult(
            boolean applied,
            String reasonCode,
            com.streamfusion.platform.media.preview.control.PreviewMaintenance.State maintenance) {}

    @PutMapping("/nodes/{nodeId}")
    public R<PolicyResult> policy(
            @PathVariable String nodeId,
            @RequestBody PreviewNodeManager.Policy input,
            HttpServletRequest request) {
        administrator();
        try {
            administration.policy(nodeId, input, AuditContextDto.from(request));
            return R.success(new PolicyResult(true, null));
        } catch (PreviewFailure failure) {
            return R.success(new PolicyResult(false, failure.reason()));
        }
    }

    public record Selection(String mode, long version) {}

    @PutMapping("/mode")
    public R<ActionResult> mode(@RequestBody Selection input, HttpServletRequest request) {
        administrator();
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        return action(
                () ->
                        administration.mode(
                                input.mode(), input.version(), AuditContextDto.from(request)));
    }

    @PostMapping("/reconcile")
    public R<ActionResult> reconcile(HttpServletRequest request) {
        long actor = administrator();
        return action(
                () -> {
                    routing.retryRecovery();
                    routing.reconcile(() -> false);
                    var result = manager.view();
                    if (result.nodeId() != null)
                        record(actor, "PREVIEW_NODE_RECONCILE", result, request);
                    return result;
                });
    }

    public record ActionResult(boolean applied, String reasonCode, PreviewNodeManager.View node) {}

    private R<ActionResult> action(java.util.function.Supplier<PreviewNodeManager.View> operation) {
        try {
            return R.success(new ActionResult(true, null, operation.get()));
        } catch (PreviewFailure failure) {
            org.slf4j.LoggerFactory.getLogger(getClass())
                    .warn("Preview node action rejected reasonCode={}", failure.reason());
            return R.success(new ActionResult(false, failure.reason(), manager.view()));
        }
    }

    private long reader() {
        long id = current.requireNormal().getId();
        if (!access.hasModuleAccess(id, "server"))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        return id;
    }

    private long administrator() {
        long id = reader();
        if (!access.isSuperAdmin(id)) throw BusinessException.error(ErrorCode.FORBIDDEN);
        return id;
    }

    private void record(
            long actor, String action, PreviewNodeManager.View result, HttpServletRequest request) {
        audit.record(
                actor,
                "PREVIEW_NODE",
                1L,
                action,
                "SUCCESS",
                null,
                AuditContextDto.from(request),
                java.util.Map.of(
                        "name",
                        result.name(),
                        "code",
                        result.nodeId(),
                        "mode",
                        result.mode(),
                        "version",
                        Long.toString(result.version())));
    }
}
