package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.storage.s3.*;
import com.streamfusion.platform.support.CameraTestSupport;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

class StorageFolderManagementTest extends CameraTestSupport {
    @MockitoBean StorageS3Client s3;
    @Autowired StorageTaskService tasks;
    @Autowired StorageWorker worker;
    private final Map<String, StorageS3Client.ObjectInfo> stored = new TreeMap<>();
    private final Map<String, StorageS3Client.ObjectInfo> unmarkedDirectories = new TreeMap<>();
    private String scope;

    @BeforeEach
    void setup() {
        stored.clear();
        unmarkedDirectories.clear();
        when(s3.protection(any(), anyString()))
                .thenReturn(
                        new StorageS3Client.BucketProtection(
                                false, false, false, false, "STORAGE_DELETE_IDENTITY_UNVERIFIED"));
        when(s3.list(any(), anyString(), anyString(), any(), anyInt(), anyBoolean()))
                .thenAnswer(
                        call -> {
                            String prefix = call.getArgument(2), cursor = call.getArgument(3);
                            int limit = call.getArgument(4),
                                    start = cursor == null ? 0 : Integer.parseInt(cursor);
                            if (Boolean.TRUE.equals(call.getArgument(5))) {
                                var directories =
                                        unmarkedDirectories.keySet().stream()
                                                .filter(
                                                        k ->
                                                                k.startsWith(prefix)
                                                                        && !k.equals(prefix))
                                                .map(
                                                        k ->
                                                                prefix
                                                                        + k.substring(
                                                                                        prefix
                                                                                                .length())
                                                                                .split("/", 2)[0]
                                                                        + "/")
                                                .distinct()
                                                .sorted()
                                                .toList();
                                int end = Math.min(directories.size(), start + limit);
                                return new StorageS3Client.ObjectPage(
                                        List.of(),
                                        directories.subList(start, end),
                                        end < directories.size() ? Integer.toString(end) : null,
                                        end < directories.size());
                            }
                            var all =
                                    stored.values().stream()
                                            .filter(o -> o.key().startsWith(prefix))
                                            .toList();
                            int end = Math.min(all.size(), start + limit);
                            return new StorageS3Client.ObjectPage(
                                    all.subList(start, end),
                                    List.of(),
                                    end < all.size() ? Integer.toString(end) : null,
                                    end < all.size());
                        });
        when(s3.head(any(), anyString(), anyString()))
                .thenAnswer(
                        call -> {
                            String key = call.getArgument(2);
                            var object =
                                    stored.containsKey(key)
                                            ? stored.get(key)
                                            : unmarkedDirectories.get(key);
                            if (object == null)
                                throw new StorageRemoteException("STORAGE_OBJECT_NOT_FOUND", 404);
                            return object;
                        });
        doAnswer(
                        call -> {
                            stored.remove(((StorageS3Client.ObjectInfo) call.getArgument(2)).key());
                            return null;
                        })
                .when(s3)
                .deleteSelected(any(), anyString(), any());
        doAnswer(
                        call -> {
                            String key = ((StorageS3Client.ObjectInfo) call.getArgument(2)).key();
                            stored.remove(key);
                            // The real S3 marker operation does not recursively remove new
                            // children.
                            if (unmarkedDirectories.keySet().stream()
                                    .noneMatch(k -> !k.equals(key) && k.startsWith(key)))
                                unmarkedDirectories.remove(key);
                            return null;
                        })
                .when(s3)
                .deleteEmptyMarker(any(), anyString(), any());
    }

    private Csrf register() throws Exception {
        var auth = admin();
        var registered =
                write(
                        post("/storage/instances"),
                        auth,
                        Map.of(
                                "name",
                                "Folder fixture",
                                "endpoint",
                                "http://127.0.0.1:8333",
                                "accessKey",
                                Map.of("action", "REPLACE", "value", "fixture-access"),
                                "secretKey",
                                Map.of("action", "REPLACE", "value", "fixture-secret"),
                                "scopes",
                                List.of(Map.of("bucket", "fixture-bucket", "prefix", "images/")),
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        201);
        scope = registered.path("scopes").get(0).path("scopeId").asText();
        return auth;
    }

    private void put(String key, String etag) {
        stored.put(
                key,
                new StorageS3Client.ObjectInfo(
                        key,
                        key.endsWith("/") ? 0 : 1,
                        etag,
                        Instant.parse("2026-01-01T00:00:00Z"),
                        "image/png",
                        null));
    }

    private JsonNode preview(Csrf auth, String prefix) throws Exception {
        var receipt =
                write(
                        post("/storage/scopes/" + scope + "/folder-deletions"),
                        auth,
                        Map.of(
                                "instanceVersion",
                                "0",
                                "scopeVersion",
                                "0",
                                "prefix",
                                prefix,
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        202);
        run();
        return read("/storage/folder-deletions/" + receipt.path("planId").asText(), auth);
    }

    private void run() {
        var task = tasks.claim("folder-test-worker");
        assertThat(task).isNotNull();
        worker.execute(task);
    }

    private JsonNode execute(Csrf auth, JsonNode plan, String key, int status) throws Exception {
        return write(
                post("/storage/folder-deletions/" + plan.path("planId").asText() + "/execute"),
                auth,
                Map.of(
                        "planVersion",
                        plan.path("version").asText(),
                        "confirmationToken",
                        plan.path("confirmationToken").asText(),
                        "idempotencyKey",
                        key),
                status);
    }

    @Test
    void deletesPaginatedSnapshotAndMarkersOnlyInsideSelectedFolderAndDoesNotReplay()
            throws Exception {
        var auth = register();
        put("images/drop/", "marker");
        for (int i = 0; i < 510; i++) put("images/drop/file-" + i, "etag-" + i);
        put("images/drop-other/keep.png", "keep");
        var plan = preview(auth, "drop/");
        assertThat(plan.path("candidateCount").asInt()).isEqualTo(511);
        String key = UUID.randomUUID().toString();
        var receipt = execute(auth, plan, key, 202);
        assertThat(execute(auth, plan, key, 202).path("taskId")).isEqualTo(receipt.path("taskId"));
        run();
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus())
                .isEqualTo("SUCCEEDED");
        assertThat(stored.keySet()).containsExactly("images/drop-other/keep.png");
        execute(auth, plan, key, 202);
        verify(s3, times(510)).deleteSelected(any(), anyString(), any());
        verify(s3, times(1)).deleteEmptyMarker(any(), anyString(), any());
    }

    @Test
    void includesUnmarkedEmptyDirectoriesAndDeletesDeepestFirst() throws Exception {
        var auth = register();
        for (String directory :
                List.of("images/drop/", "images/drop/nested/", "images/drop/nested/empty/"))
            unmarkedDirectories.put(
                    directory,
                    new StorageS3Client.ObjectInfo(
                            directory,
                            0,
                            "empty",
                            Instant.parse("2026-01-01T00:00:00Z"),
                            "application/x-directory",
                            null));
        put("images/drop-other/keep.png", "keep");
        var plan = preview(auth, "drop/");
        assertThat(plan.path("candidateCount").asInt()).isEqualTo(3);
        var receipt = execute(auth, plan, UUID.randomUUID().toString(), 202);
        run();
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus())
                .isEqualTo("SUCCEEDED");
        assertThat(unmarkedDirectories).isEmpty();
        assertThat(stored.keySet()).containsExactly("images/drop-other/keep.png");
        var order = inOrder(s3);
        for (String directory :
                List.of("images/drop/nested/empty/", "images/drop/nested/", "images/drop/"))
            order.verify(s3)
                    .deleteEmptyMarker(any(), anyString(), argThat(o -> o.key().equals(directory)));
        verify(s3, never()).deleteSelected(any(), anyString(), any());
    }

    @Test
    void aNewUnmarkedDirectoryInvalidatesTheSnapshotBeforeAnyDelete() throws Exception {
        var auth = register();
        put("images/drop/a.png", "old");
        var plan = preview(auth, "drop/");
        String added = "images/drop/arrived/";
        unmarkedDirectories.put(
                added,
                new StorageS3Client.ObjectInfo(
                        added,
                        0,
                        "new",
                        Instant.parse("2026-01-01T00:00:00Z"),
                        "application/x-directory",
                        null));
        var receipt = execute(auth, plan, UUID.randomUUID().toString(), 202);
        run();
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus()).isEqualTo("FAILED");
        verify(s3, never()).deleteSelected(any(), anyString(), any());
        verify(s3, never()).deleteEmptyMarker(any(), anyString(), any());
        assertThat(stored).containsKey("images/drop/a.png");
        assertThat(unmarkedDirectories).containsKey(added);
    }

    @Test
    void aDirectoryStillPresentAfterSuccessfulRemoteResponsesIsNotReportedAsDeleted()
            throws Exception {
        var auth = register();
        String directory = "images/drop/";
        unmarkedDirectories.put(
                directory,
                new StorageS3Client.ObjectInfo(
                        directory,
                        0,
                        "empty",
                        Instant.parse("2026-01-01T00:00:00Z"),
                        "application/x-directory",
                        null));
        var plan = preview(auth, "drop/");
        doNothing().when(s3).deleteEmptyMarker(any(), anyString(), any());
        var receipt = execute(auth, plan, UUID.randomUUID().toString(), 202);
        run();
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus()).isEqualTo("PARTIAL");
        assertThat(unmarkedDirectories).containsKey(directory);
    }

    @Test
    void changedSnapshotStopsBeforeFirstDeleteAndRootTraversalAreRejected() throws Exception {
        var auth = register();
        put("images/drop/a.png", "old");
        var plan = preview(auth, "drop/");
        put("images/drop/a.png", "new");
        var receipt = execute(auth, plan, UUID.randomUUID().toString(), 202);
        run();
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus()).isEqualTo("FAILED");
        verify(s3, never()).deleteSelected(any(), anyString(), any());
        for (String prefix : List.of("", "../", "a//b/", "a\\b/"))
            write(
                    post("/storage/scopes/" + scope + "/folder-deletions"),
                    auth,
                    Map.of(
                            "instanceVersion",
                            "0",
                            "scopeVersion",
                            "0",
                            "prefix",
                            prefix,
                            "idempotencyKey",
                            UUID.randomUUID().toString()),
                    400);
    }

    @Test
    void newArrivalDuringDeleteIsPreservedAndReportedPartial() throws Exception {
        var auth = register();
        put("images/drop/a.png", "a");
        var plan = preview(auth, "drop/");
        doAnswer(
                        call -> {
                            stored.remove("images/drop/a.png");
                            put("images/drop/new.png", "new");
                            return null;
                        })
                .when(s3)
                .deleteSelected(any(), anyString(), any());
        var receipt = execute(auth, plan, UUID.randomUUID().toString(), 202);
        run();
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus()).isEqualTo("PARTIAL");
        assertThat(stored.keySet()).containsExactly("images/drop/new.png");
    }

    @Test
    void lostLeaseOnDestructiveJobBecomesUnknownWithoutReplay() throws Exception {
        var auth = register();
        put("images/drop/a.png", "a");
        var plan = preview(auth, "drop/");
        var receipt = execute(auth, plan, UUID.randomUUID().toString(), 202);
        var claimed = tasks.claim("lost-worker");
        jdbc.update(
                "UPDATE storage_task SET lease_until=? WHERE id=?",
                StorageRules.now().minusMinutes(1),
                claimed.getId());
        tasks.recover();
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus()).isEqualTo("UNKNOWN");
        assertThat(tasks.claim("next-worker")).isNull();
        verify(s3, never()).deleteSelected(any(), anyString(), any());
    }

    @Test
    void versionedBucketIsRefusedWithoutDeletingAnything() throws Exception {
        var auth = register();
        when(s3.protection(any(), anyString()))
                .thenReturn(
                        new StorageS3Client.BucketProtection(
                                true, false, false, false, "STORAGE_DELETE_IDENTITY_UNVERIFIED"));
        assertThat(preview(auth, "drop/").path("status").asText()).isEqualTo("FAILED");
        verify(s3, never()).deleteSelected(any(), anyString(), any());
    }
}
