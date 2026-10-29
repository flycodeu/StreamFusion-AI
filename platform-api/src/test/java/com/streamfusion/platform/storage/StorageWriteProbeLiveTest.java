package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put;

import com.streamfusion.platform.support.CameraTestSupport;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

/** Opt-in HTTPS SeaweedFS capability test; the fixture owns its temporary bucket and key. */
class StorageWriteProbeLiveTest extends CameraTestSupport {
    @Autowired private StorageTaskService tasks;
    @Autowired private StorageWorker worker;

    @DynamicPropertySource
    static void isolatedEndpoint(DynamicPropertyRegistry registry) {
        String endpoint = System.getenv("SF_STORAGE_LIVE_ENDPOINT");
        if (endpoint != null) registry.add("platform.storage.allowed-endpoints[0]", () -> endpoint);
    }

    @Test
    void realWriteReadDeleteProbeEnablesOneDefaultScope() throws Exception {
        String endpoint = System.getenv("SF_STORAGE_LIVE_ENDPOINT");
        String bucket = System.getenv("SF_STORAGE_LIVE_BUCKET");
        String prefix = System.getenv("SF_STORAGE_LIVE_PREFIX");
        String access = System.getenv("SF_STORAGE_LIVE_ACCESS");
        String secret = System.getenv("SF_STORAGE_LIVE_SECRET");
        Assumptions.assumeTrue(
                endpoint != null
                        && bucket != null
                        && prefix != null
                        && access != null
                        && secret != null,
                "Run through verify-platform-presign.py");
        var auth = admin();
        var saved =
                write(
                        post("/storage/instances"),
                        auth,
                        Map.of(
                                "name", "isolated-inspection-write-probe",
                                "endpoint", endpoint,
                                "region", "us-east-1",
                                "accessKey", Map.of("action", "REPLACE", "value", access),
                                "secretKey", Map.of("action", "REPLACE", "value", secret),
                                "scopes",
                                        List.of(
                                                Map.of(
                                                        "bucket", bucket,
                                                        "prefix", prefix,
                                                        "probePrefix", prefix + ".probe/")),
                                "idempotencyKey", UUID.randomUUID().toString()),
                        201);
        String id = saved.path("instanceId").asText();
        String scope = saved.path("scopes").get(0).path("scopeId").asText();
        var check =
                write(
                        post("/storage/instances/" + id + "/checks"),
                        auth,
                        Map.of(
                                "version",
                                "0",
                                "scopeId",
                                scope,
                                "mode",
                                "READ_WRITE",
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        202);
        var task = tasks.claim("isolated-live-probe");
        assertThat(task.getId().toString()).isEqualTo(check.path("taskId").asText());
        worker.execute(task);
        assertThat(read("/storage/tasks/" + task.getId(), auth).path("status").asText())
                .isEqualTo("SUCCEEDED");
        assertThat(read("/storage/instances/" + id, auth).path("writeCheckStatus").asText())
                .isEqualTo("PASSED");
        var enabled =
                write(
                        put("/storage/instances/" + id + "/write-state"),
                        auth,
                        Map.of("version", "0", "writeEnabled", true),
                        200);
        assertThat(enabled.path("writeEnabled").asBoolean()).isTrue();
        var selected =
                write(
                        put("/storage/default-target"),
                        auth,
                        Map.of(
                                "version", "0",
                                "scopeId", scope,
                                "scopeVersion", "0",
                                "instanceVersion", "1"),
                        200);
        assertThat(selected.path("scopeId").asText()).isEqualTo(scope);
    }
}
