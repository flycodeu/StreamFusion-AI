package com.streamfusion.platform.storage;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import jakarta.servlet.http.HttpServletRequest;
import java.net.URI;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

/** Configuration HTTP boundary; remote operations belong to the object/task controllers. */
@RestController
@RequestMapping("/storage")
@ModuleAccess("storage")
@RequiredArgsConstructor
public class StorageController {
    private final StorageConfigurationService configuration;
    private final StorageViews views;
    private final StorageSecrets secrets;

    @GetMapping("/options")
    public R<?> options() {
        return R.success(
                Map.of(
                        "networkPolicies",
                        List.of(),
                        "credentialProtectionReady",
                        secrets.ready(),
                        "deleteReady",
                        false,
                        "deleteReason",
                        "STORAGE_DELETE_IDENTITY_UNVERIFIED",
                        "provider",
                        "SEAWEEDFS",
                        "maxInstances",
                        100));
    }

    @GetMapping("/instances/page")
    public R<?> page(
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "20") int size,
            @RequestParam(required = false) String keyword,
            @RequestParam(required = false) Boolean writeEnabled,
            @RequestParam(required = false) String accessStatus) {
        return R.success(configuration.page(page, size, keyword, writeEnabled, accessStatus));
    }

    @PostMapping("/instances")
    public ResponseEntity<R<?>> create(
            @RequestBody StorageCommands.Instance input, HttpServletRequest request) {
        var result = configuration.create(input, AuditContextDto.from(request));
        return ResponseEntity.created(URI.create("/storage/instances/" + result.get("instanceId")))
                .body(R.success(result));
    }

    @GetMapping("/instances/{id}")
    public ResponseEntity<R<?>> detail(@PathVariable long id) {
        var view = configuration.detail(id);
        return ResponseEntity.ok().eTag(view.get("version").toString()).body(R.success(view));
    }

    @PutMapping("/instances/{id}")
    public R<?> edit(
            @PathVariable long id,
            @RequestBody StorageCommands.Instance input,
            HttpServletRequest request) {
        return R.success(configuration.update(id, input, AuditContextDto.from(request)));
    }

    @DeleteMapping("/instances/{id}")
    public R<?> remove(
            @PathVariable long id,
            @RequestHeader(value = "If-Match", required = false) String version,
            HttpServletRequest request) {
        configuration.remove(id, version(version), AuditContextDto.from(request));
        return R.success();
    }

    @PutMapping("/instances/{id}/write-state")
    public R<?> writeState(@PathVariable long id, @RequestBody StorageCommands.WriteState input) {
        return R.success(configuration.writeState(id, input));
    }

    @GetMapping("/default-target")
    public R<?> defaultTarget() {
        return R.success(views.defaultTarget(configuration.defaultRow()));
    }

    @PutMapping("/default-target")
    public R<?> defaultTarget(@RequestBody StorageCommands.DefaultTarget input) {
        return R.success(configuration.setDefault(input));
    }

    @GetMapping("/instances/{id}/scopes")
    public R<?> scopes(@PathVariable long id) {
        configuration.require(id);
        return R.success(configuration.scopes(id).stream().map(views::scope).toList());
    }

    public record NewScope(
            Long instanceVersion,
            String bucket,
            String prefix,
            String displayName,
            String probePrefix,
            String idempotencyKey) {}

    @PostMapping("/instances/{id}/scopes")
    public ResponseEntity<R<?>> addScope(@PathVariable long id, @RequestBody NewScope input) {
        if (input.instanceVersion() == null) throw StorageRules.invalid();
        var result =
                configuration.addScope(
                        id,
                        input.instanceVersion(),
                        new StorageCommands.Scope(
                                input.bucket(),
                                input.prefix(),
                                input.displayName(),
                                input.probePrefix()),
                        input.idempotencyKey());
        return ResponseEntity.created(URI.create("/storage/instances/" + id + "/scopes"))
                .body(R.success(result));
    }

    @PutMapping("/scopes/{id}")
    public R<?> editScope(@PathVariable long id, @RequestBody StorageCommands.ScopeEdit input) {
        return R.success(configuration.editScope(id, input));
    }

    @DeleteMapping("/scopes/{id}")
    public R<?> removeScope(
            @PathVariable long id,
            @RequestHeader(value = "If-Match", required = false) String version) {
        configuration.removeScope(id, version(version));
        return R.success();
    }

    static long version(String value) {
        if (value == null)
            throw com.streamfusion.platform.common.exception.BusinessException.error(
                    com.streamfusion.platform.common.exception.ErrorCode.PRECONDITION_REQUIRED);
        if (!value.matches("\"[0-9]{1,19}\"")) throw StorageRules.invalid();
        try {
            return Long.parseLong(value.substring(1, value.length() - 1));
        } catch (NumberFormatException ex) {
            throw StorageRules.invalid();
        }
    }
}
