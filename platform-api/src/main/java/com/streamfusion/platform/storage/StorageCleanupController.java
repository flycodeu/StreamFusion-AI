package com.streamfusion.platform.storage;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import java.net.URI;
import lombok.RequiredArgsConstructor;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/storage")
@ModuleAccess("storage")
@RequiredArgsConstructor
public class StorageCleanupController {
    private final StorageCleanupService cleanup;

    @PostMapping("/scopes/{id}/cleanup-plans")
    public ResponseEntity<R<?>> preview(
            @PathVariable long id, @RequestBody StorageCommands.Cleanup input) {
        var result = cleanup.preview(id, input);
        return ResponseEntity.accepted()
                .location(URI.create("/storage/cleanup-plans/" + result.get("planId")))
                .body(R.success(result));
    }

    @GetMapping("/cleanup-plans/{id}")
    public R<?> plan(@PathVariable long id) {
        return R.success(cleanup.detail(id));
    }

    @GetMapping("/cleanup-plans/{id}/items")
    public R<?> items(
            @PathVariable long id,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "20") int size) {
        return R.success(cleanup.items(id, page, size));
    }

    @PostMapping("/cleanup-plans/{id}/execute")
    public R<?> execute(@PathVariable long id, @RequestBody StorageCommands.Execute input) {
        cleanup.execute(id, input);
        return R.success();
    }

    @GetMapping("/cleanup-plans/{id}/execution")
    public R<?> execution(@PathVariable long id) {
        cleanup.require(id);
        throw com.streamfusion.platform.common.exception.BusinessException.error(
                com.streamfusion.platform.common.exception.ErrorCode.NOT_FOUND);
    }

    @GetMapping("/scopes/{id}/cleanup-rules")
    public R<?> rules(@PathVariable long id) {
        return R.success(cleanup.rules(id));
    }

    @PostMapping("/scopes/{id}/cleanup-rules")
    public ResponseEntity<R<?>> createRule(
            @PathVariable long id, @RequestBody StorageCommands.Rule input) {
        var rule = cleanup.saveRule(id, null, input);
        return ResponseEntity.created(URI.create("/storage/cleanup-rules/" + rule.getId()))
                .body(R.success(rule));
    }

    @PutMapping("/scopes/{scopeId}/cleanup-rules/{id}")
    public R<?> updateRule(
            @PathVariable long scopeId,
            @PathVariable long id,
            @RequestBody StorageCommands.Rule input) {
        return R.success(cleanup.saveRule(scopeId, id, input));
    }

    @PutMapping("/cleanup-rules/{id}/enabled")
    public R<?> enabled(@PathVariable long id, @RequestBody StorageCommands.Enabled input) {
        return R.success(cleanup.enabled(id, input));
    }

    @DeleteMapping("/cleanup-rules/{id}")
    public R<?> removeRule(
            @PathVariable long id,
            @RequestHeader(value = "If-Match", required = false) String version) {
        cleanup.removeRule(id, StorageController.version(version));
        return R.success();
    }
}
