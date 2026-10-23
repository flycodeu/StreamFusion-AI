package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.streamfusion.platform.storage.mapper.StorageInstanceMapper;
import com.streamfusion.platform.storage.s3.StorageRemoteException;
import com.streamfusion.platform.storage.s3.StorageS3Client;
import com.streamfusion.platform.support.CameraTestSupport;
import java.nio.file.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.autoconfigure.web.servlet.MockMvcPrint;

@EnabledIfEnvironmentVariable(named = "SF_STORAGE_LOCAL_ACCEPTANCE", matches = "true")
@AutoConfigureMockMvc(print = MockMvcPrint.NONE, printOnlyOnFailure = false)
class StorageLocalFolderAcceptanceTest extends CameraTestSupport {
    @Autowired StorageS3Client s3;
    @Autowired StorageInstanceMapper instances;
    @Autowired StorageTaskService tasks;
    @Autowired StorageWorker worker;

    @Test
    void localSeaweedPaginatesAndDeletesOnlyConfirmedFolderIncludingNestedMarkers()
            throws Exception {
        String prefix = "_storage_fix_10_" + UUID.randomUUID() + "/";
        assertThat(prefix).matches("_storage_fix_10_[0-9a-f-]{36}/");
        String credentialFile = System.getenv("SF_STORAGE_LOCAL_S3_CONFIG");
        assertThat(credentialFile)
                .as("Set SF_STORAGE_LOCAL_S3_CONFIG for local acceptance")
                .isNotBlank();
        var credentials =
                json.readTree(Files.readAllBytes(Path.of(credentialFile)))
                        .path("identities")
                        .get(0)
                        .path("credentials")
                        .get(0);
        var auth = admin();
        var registered =
                write(
                        post("/storage/instances"),
                        auth,
                        Map.of(
                                "name",
                                "Local acceptance",
                                "endpoint",
                                "http://127.0.0.1:8333",
                                "accessKey",
                                Map.of(
                                        "action",
                                        "REPLACE",
                                        "value",
                                        credentials.path("accessKey").asText()),
                                "secretKey",
                                Map.of(
                                        "action",
                                        "REPLACE",
                                        "value",
                                        credentials.path("secretKey").asText()),
                                "scopes",
                                List.of(
                                        Map.of(
                                                "bucket",
                                                "streamfusion-evidence",
                                                "prefix",
                                                prefix)),
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        201);
        String scope = registered.path("scopes").get(0).path("scopeId").asText();

        var instance = instances.selectById(registered.path("instanceId").asLong());
        String bucket = "streamfusion-evidence";
        for (String directory : List.of("drop/", "drop/nested/", "drop/empty/"))
            s3.putIfAbsent(
                    instance, bucket, prefix + directory, new byte[0], "application/x-directory");
        for (int i = 0; i < 55; i++)
            s3.putIfAbsent(
                    instance,
                    bucket,
                    prefix + "drop/" + String.format("%03d.png", i),
                    new byte[] {1, 2, 3},
                    "image/png");
        s3.putIfAbsent(
                instance,
                bucket,
                prefix + "drop/nested/中文+%25.png",
                new byte[] {4, 5},
                "image/png");
        s3.putIfAbsent(
                instance, bucket, prefix + "drop-other/keep.png", new byte[] {6}, "image/png");
        // Reproduce the real failure: Filer directories that have no S3 marker are absent from
        // the flat object listing, but still appear in CommonPrefixes after all objects are gone.
        var filer =
                java.net.http.HttpClient.newBuilder()
                        .connectTimeout(java.time.Duration.ofSeconds(3))
                        .build();
        var directory =
                filer.send(
                        java.net.http.HttpRequest.newBuilder(
                                        java.net.URI.create(
                                                "http://127.0.0.1:8888/buckets/"
                                                        + bucket
                                                        + "/"
                                                        + prefix
                                                        + "drop/unmarked/nested/?mkdir=true"))
                                .timeout(java.time.Duration.ofSeconds(5))
                                .POST(java.net.http.HttpRequest.BodyPublishers.noBody())
                                .build(),
                        java.net.http.HttpResponse.BodyHandlers.discarding());
        assertThat(directory.statusCode()).isEqualTo(201);
        assertThat(s3.list(instance, bucket, prefix + "drop/unmarked/", null, 10, false).items())
                .isEmpty();
        assertThat(s3.head(instance, bucket, prefix + "drop/unmarked/nested/").size()).isZero();
        String cursor = null;
        int pages = 0;
        Set<String> files = new HashSet<>();
        do {
            var request =
                    get("/storage/scopes/" + scope + "/objects")
                            .param("prefix", "drop/")
                            .param("limit", "20")
                            .session(auth.session());
            if (cursor != null) request.param("cursor", cursor);
            var response = mvc.perform(request).andReturn().getResponse();
            assertThat(response.getStatus()).isEqualTo(200);
            var data = json.readTree(response.getContentAsByteArray()).path("data");
            for (var file : data.path("items"))
                if (!file.path("objectKey").asText().endsWith("/"))
                    assertThat(files.add(file.path("objectKey").asText())).isTrue();
            cursor = data.path("nextCursor").isNull() ? null : data.path("nextCursor").asText();
            pages++;
        } while (cursor != null);
        assertThat(files).hasSize(55);
        assertThat(pages).isGreaterThanOrEqualTo(3);
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
                                "drop/",
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        202);
        run();
        var plan = read("/storage/folder-deletions/" + receipt.path("planId").asText(), auth);
        assertThat(plan.path("status").asText()).isEqualTo("READY");
        assertThat(plan.path("candidateCount").asInt()).isEqualTo(61);
        var deletion =
                write(
                        post(
                                "/storage/folder-deletions/"
                                        + receipt.path("planId").asText()
                                        + "/execute"),
                        auth,
                        Map.of(
                                "planVersion",
                                plan.path("version").asText(),
                                "confirmationToken",
                                plan.path("confirmationToken").asText(),
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        202);
        run();
        var task = tasks.require(deletion.path("taskId").asLong());
        assertThat(task.getStatus()).isEqualTo("SUCCEEDED");
        assertThat(s3.list(instance, bucket, prefix + "drop/", null, 100, false).items()).isEmpty();
        assertThat(s3.list(instance, bucket, prefix, null, 100, true).commonPrefixes())
                .doesNotContain(prefix + "drop/");
        assertThatThrownBy(() -> s3.head(instance, bucket, prefix + "drop/"))
                .isInstanceOfSatisfying(
                        StorageRemoteException.class,
                        absent -> assertThat(absent.status()).isEqualTo(404));
        var sibling = s3.head(instance, bucket, prefix + "drop-other/keep.png");
        assertThat(sibling.size()).isEqualTo(1);
        s3.deleteSelected(instance, bucket, sibling);
        for (String empty : List.of(prefix + "drop-other/", prefix)) {
            try {
                s3.deleteEmptyMarker(instance, bucket, s3.head(instance, bucket, empty));
            } catch (StorageRemoteException absent) {
                if (absent.status() != 404) throw absent;
            }
        }
        assertThat(s3.list(instance, bucket, prefix, null, 1, true).commonPrefixes()).isEmpty();
        Files.createDirectories(Path.of(".run/storage-fix-10"));
        Files.writeString(
                Path.of(".run/storage-fix-10/live-folder-result.json"),
                json.writerWithDefaultPrettyPrinter()
                        .writeValueAsString(
                                Map.of(
                                        "status",
                                        "PASS",
                                        "prefix",
                                        prefix,
                                        "filePages",
                                        pages,
                                        "listedFiles",
                                        files.size(),
                                        "candidateCount",
                                        plan.path("candidateCount").asText(),
                                        "deleteResult",
                                        json.readTree(task.getResultJson()),
                                        "siblingPreserved",
                                        true,
                                        "rootDirectoryAbsent",
                                        true,
                                        "existingFilesTouched",
                                        false,
                                        "database",
                                        "isolated H2")));
    }

    private void run() {
        var task = tasks.claim("local-folder-worker");
        assertThat(task).isNotNull();
        worker.execute(task);
    }
}
