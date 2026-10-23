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
public class StorageOperationController {
    private final StorageTaskService tasks;
    private final StorageConfigurationService configuration;
    private final StorageJson json;
    private final StorageObjectService objects;
    private final StorageInspectionService inspection;
    private final StorageFileService files;
    private final StorageFolderService folders;

    @PostMapping("/scopes/{id}/folder-deletions")
    public ResponseEntity<R<?>> previewFolder(
            @PathVariable long id, @RequestBody StorageCommands.FolderDeletion input) {
        return ResponseEntity.accepted().body(R.success(folders.preview(id, input)));
    }

    @GetMapping("/folder-deletions/{id}")
    public R<?> folderDeletion(@PathVariable long id) {
        return R.success(folders.detail(id));
    }

    @PostMapping("/folder-deletions/{id}/execute")
    public ResponseEntity<R<?>> deleteFolder(
            @PathVariable long id, @RequestBody StorageCommands.Execute input) {
        return accepted(folders.execute(id, input));
    }

    @PostMapping(value = "/scopes/{id}/objects/upload", consumes = "multipart/form-data")
    public ResponseEntity<R<?>> upload(
            @PathVariable long id,
            @RequestParam Long instanceVersion,
            @RequestParam Long scopeVersion,
            @RequestParam(defaultValue = "") String relativePrefix,
            @RequestParam String idempotencyKey,
            @RequestPart("file") org.springframework.web.multipart.MultipartFile file)
            throws java.io.IOException {
        return ResponseEntity.status(201)
                .body(
                        R.success(
                                files.upload(
                                        id,
                                        instanceVersion,
                                        scopeVersion,
                                        relativePrefix,
                                        idempotencyKey,
                                        file)));
    }

    @PostMapping("/scopes/{id}/objects/folder")
    public ResponseEntity<R<?>> folder(
            @PathVariable long id, @RequestBody StorageCommands.Folder input) {
        return ResponseEntity.status(201).body(R.success(files.folder(id, input)));
    }

    @DeleteMapping("/scopes/{id}/object")
    public R<?> deleteObject(
            @PathVariable long id, @RequestBody StorageCommands.DeleteObject input) {
        return R.success(files.delete(id, input));
    }

    @PostMapping("/instances/{id}/checks")
    public ResponseEntity<R<?>> check(
            @PathVariable long id, @RequestBody StorageCommands.Task input) {
        return accepted(tasks.submit(id, "CHECK", input));
    }

    @PostMapping("/instances/{id}/usage-scans")
    public ResponseEntity<R<?>> scan(
            @PathVariable long id, @RequestBody StorageCommands.Task input) {
        return accepted(tasks.submit(id, "USAGE_SCAN", input));
    }

    @GetMapping("/instances/{id}/tasks/page")
    public R<?> taskPage(
            @PathVariable long id,
            @RequestParam(defaultValue = "1") int page,
            @RequestParam(defaultValue = "20") int size) {
        configuration.require(id);
        return R.success(tasks.page(id, page, size));
    }

    @GetMapping("/tasks/{id}")
    public R<?> task(@PathVariable long id) {
        return R.success(tasks.detail(id));
    }

    @PostMapping("/tasks/{id}/cancel")
    public ResponseEntity<R<?>> cancel(
            @PathVariable long id, @RequestBody StorageCommands.Task input) {
        return accepted(tasks.cancel(id, input.version()));
    }

    @PostMapping("/tasks/{id}/resume")
    public ResponseEntity<R<?>> resume(
            @PathVariable long id, @RequestBody StorageCommands.Task input) {
        return accepted(tasks.resume(id, input.version()));
    }

    @GetMapping("/task-receipts")
    public R<?> receipt(
            @RequestParam String idempotencyKey,
            @RequestParam String action,
            @RequestParam long targetId) {
        return R.success(tasks.receipt(action, idempotencyKey, targetId));
    }

    @GetMapping("/instances/{id}/observations")
    public R<?> observations(@PathVariable long id) {
        return R.success(json.read(configuration.require(id).getObservation()));
    }

    @GetMapping("/instances/{id}/usage")
    public R<?> usage(@PathVariable long id) {
        return R.success(
                java.util.Map.of(
                        "lastComplete",
                        json.read(configuration.require(id).getUsageSnapshot()),
                        "currentRun",
                        tasks.page(id, 1, 1)));
    }

    @GetMapping("/scopes/{id}/objects")
    public R<?> objects(
            @PathVariable long id,
            @RequestParam(defaultValue = "") String prefix,
            @RequestParam(required = false) String cursor,
            @RequestParam(defaultValue = "50") int limit) {
        return R.success(objects.list(id, prefix, cursor, limit));
    }

    @GetMapping("/scopes/{id}/object")
    public R<?> object(@PathVariable long id, @RequestParam String key) {
        return R.success(objects.detail(id, key));
    }

    @GetMapping("/scopes/{id}/object-content")
    public ResponseEntity<
                    org.springframework.web.servlet.mvc.method.annotation.StreamingResponseBody>
            content(
                    @PathVariable long id,
                    @RequestParam String key,
                    @RequestParam String objectToken,
                    @RequestParam(defaultValue = "inline") String disposition,
                    @RequestHeader(value = "Range", required = false) String range) {
        return objects.content(id, key, objectToken, disposition, range);
    }

    @PostMapping("/connection-tests")
    public R<?> draft(
            @RequestBody StorageCommands.Instance input,
            @RequestParam(required = false) Long instanceId) {
        var row = configuration.draft(instanceId, input);
        if (input.scopes() == null || input.scopes().size() != 1) throw StorageRules.invalid();
        var selected = input.scopes().get(0);
        var scope = new com.streamfusion.platform.storage.entity.StorageScope();
        scope.setId(0L);
        scope.setVersion(0L);
        scope.setBucket(StorageRules.bucket(selected.bucket()));
        scope.setPrefix(StorageRules.prefix(selected.prefix()));
        return R.success(inspection.checkScope(row, scope, "READ_ONLY_DRAFT"));
    }

    private ResponseEntity<R<?>> accepted(java.util.Map<String, Object> receipt) {
        return ResponseEntity.accepted()
                .location(URI.create("/storage/tasks/" + receipt.get("taskId")))
                .body(R.success(receipt));
    }
}
