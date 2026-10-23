package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.storage.s3.StorageNetwork;
import com.streamfusion.platform.support.CameraTestSupport;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.CopyOnWriteArrayList;
import org.junit.jupiter.api.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/** Real HTTP signing/parsing/streaming and maintained SQL in isolated H2; no real S3 objects. */
class StorageManagementTest extends CameraTestSupport {
    @MockitoBean StorageNetwork network;
    @Autowired StorageTaskService tasks;
    @Autowired StorageWorker worker;
    @Autowired StorageHistoryService history;
    private HttpServer server;
    private final List<String> requests = new CopyOnWriteArrayList<>();
    private volatile String objectKey = "evidence/中文+%25.jpg";
    private volatile boolean empty;

    @BeforeEach
    void s3() throws Exception {
        when(network.resolve(any(), anyString())).thenReturn(InetAddress.getLoopbackAddress());
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/",
                exchange -> {
                    String method = exchange.getRequestMethod();
                    String query = exchange.getRequestURI().getRawQuery();
                    requests.add(method + " " + exchange.getRequestURI());
                    assertThat(exchange.getRequestHeaders().getFirst("Authorization"))
                            .startsWith("AWS4-HMAC-SHA256 Credential=test-access/");
                    byte[] body;
                    int status = 200;
                    if (query != null && query.contains("list-type=2")) {
                        String contents =
                                empty
                                        ? ""
                                        : "<Contents><Key>"
                                                + URLEncoder.encode(
                                                                objectKey, StandardCharsets.UTF_8)
                                                        .replace("+", "%20")
                                                + "</Key><Size>6</Size><ETag>&quot;sample&quot;</ETag><LastModified>2026-01-01T00:00:00Z</LastModified></Contents>";
                        body =
                                ("<ListBucketResult><IsTruncated>false</IsTruncated>"
                                                + contents
                                                + "</ListBucketResult>")
                                        .getBytes(StandardCharsets.UTF_8);
                    } else if (query != null)
                        body = "<Configuration/>".getBytes(StandardCharsets.UTF_8);
                    else {
                        exchange.getResponseHeaders().set("ETag", "\"sample\"");
                        exchange.getResponseHeaders()
                                .set("Last-Modified", "Thu, 01 Jan 2026 00:00:00 GMT");
                        exchange.getResponseHeaders().set("Content-Type", "image/jpeg");
                        body = "abcdef".getBytes(StandardCharsets.UTF_8);
                        if (method.equals("HEAD")) {
                            exchange.getResponseHeaders().set("Content-Length", "6");
                            exchange.sendResponseHeaders(200, -1);
                            exchange.close();
                            return;
                        }
                        if (exchange.getRequestHeaders().getFirst("Range") != null) {
                            assertThat(exchange.getRequestHeaders().getFirst("If-Match"))
                                    .isEqualTo("\"sample\"");
                            status = 206;
                            body = "bcd".getBytes(StandardCharsets.UTF_8);
                            exchange.getResponseHeaders().set("Content-Range", "bytes 1-3/6");
                        }
                    }
                    exchange.sendResponseHeaders(status, body.length);
                    exchange.getResponseBody().write(body);
                    exchange.close();
                });
        server.start();
    }

    @AfterEach
    void stop() {
        if (server != null) server.stop(0);
    }

    Map<String, Object> input() {
        return new LinkedHashMap<>(
                Map.of(
                        "name",
                        "Isolated storage",
                        "endpoint",
                        "http://10.0.1.5:" + server.getAddress().getPort(),
                        "networkPolicyKey",
                        "test",
                        "accessKey",
                        Map.of("action", "REPLACE", "value", "test-access"),
                        "secretKey",
                        Map.of("action", "REPLACE", "value", "private-storage-secret"),
                        "scopes",
                        List.of(
                                Map.of(
                                        "bucket",
                                        "evidence-bucket",
                                        "prefix",
                                        "evidence/",
                                        "probePrefix",
                                        "evidence/.probe/")),
                        "idempotencyKey",
                        UUID.randomUUID().toString()));
    }

    JsonNode register(Csrf auth) throws Exception {
        return write(post("/storage/instances"), auth, input(), 201);
    }

    @Test
    void consoleAddressRoundTripsAndCameraPoliciesAreNoLongerRequiredOrExposed() throws Exception {
        var auth = admin();
        var body = input();
        body.remove("networkPolicyKey");
        body.put("endpoint", "http://127.0.0.1:8333");
        body.put("managementUrl", "http://127.0.0.1:23646");
        var saved = write(post("/storage/instances"), auth, body, 201);
        assertThat(saved.path("managementUrl").asText()).isEqualTo("http://127.0.0.1:23646");
        assertThat(read("/storage/options", auth).path("networkPolicies")).isEmpty();
        body.put("accessKey", Map.of("action", "KEEP"));
        body.put("secretKey", Map.of("action", "KEEP"));
        body.put("version", "0");
        body.put("managementUrl", "http://127.0.0.1:23646/admin");
        var updated =
                write(
                        put("/storage/instances/" + saved.path("instanceId").asText()),
                        auth,
                        body,
                        200);
        assertThat(updated.path("managementUrl").asText()).endsWith("/admin");
        assertThat(updated.path("version").asText()).isEqualTo("1");
        body.put("version", "1");
        body.put("managementUrl", "javascript:alert(1)");
        write(put("/storage/instances/" + saved.path("instanceId").asText()), auth, body, 400);
        assertThat(requests).isEmpty();
    }

    JsonNode finish(JsonNode receipt, Csrf auth) throws Exception {
        var claimed = tasks.claim("isolated-test");
        assertThat(claimed.getId().toString()).isEqualTo(receipt.path("taskId").asText());
        worker.execute(claimed);
        return read("/storage/tasks/" + claimed.getId(), auth);
    }

    @Test
    void registrationReplayEncryptsAndNeverExposesCredentials() throws Exception {
        var auth = admin();
        var body = input();
        var saved = write(post("/storage/instances"), auth, body, 201);
        assertThat(write(post("/storage/instances"), auth, body, 201).path("instanceId"))
                .isEqualTo(saved.path("instanceId"));
        assertThat(saved.toString())
                .doesNotContain(
                        "private-storage-secret", "test-access", "secretCiphertext", "secretNonce");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_instance", Integer.class))
                .isEqualTo(1);
        byte[] encrypted =
                jdbc.queryForObject("SELECT secret_ciphertext FROM storage_instance", byte[].class);
        assertThat(new String(encrypted, StandardCharsets.UTF_8))
                .doesNotContain("private-storage-secret");
        body.put("name", "Changed request");
        write(post("/storage/instances"), auth, body, 409);
    }

    @Test
    void draftDoesNotSaveAndOverlapAndVersionAreRejected() throws Exception {
        var auth = admin();
        assertThat(
                        write(post("/storage/connection-tests"), auth, input(), 200)
                                .path("status")
                                .asText())
                .isEqualTo("ACCESSIBLE");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_instance", Integer.class))
                .isZero();
        var saved = register(auth);
        String id = saved.path("instanceId").asText();
        write(
                post("/storage/instances/" + id + "/scopes"),
                auth,
                Map.of(
                        "instanceVersion",
                        "0",
                        "bucket",
                        "evidence-bucket",
                        "prefix",
                        "evidence/child/",
                        "idempotencyKey",
                        UUID.randomUUID().toString()),
                409);
        var update = input();
        update.put("version", "999");
        write(put("/storage/instances/" + id), auth, update, 409);
    }

    @Test
    void checkAndScanPublishActualCountsAndProtectNonemptyUnregister() throws Exception {
        var auth = admin();
        var saved = register(auth);
        String id = saved.path("instanceId").asText();
        var check =
                write(
                        post("/storage/instances/" + id + "/checks"),
                        auth,
                        Map.of(
                                "version",
                                "0",
                                "mode",
                                "READ_ONLY",
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        202);
        assertThat(finish(check, auth).path("status").asText()).isEqualTo("SUCCEEDED");
        var scan =
                write(
                        post("/storage/instances/" + id + "/usage-scans"),
                        auth,
                        Map.of("version", "0", "idempotencyKey", UUID.randomUUID().toString()),
                        202);
        assertThat(finish(scan, auth).path("result").path("logicalBytes").asText()).isEqualTo("6");
        var detail = read("/storage/instances/" + id, auth);
        assertThat(detail.path("freshness").asText()).isEqualTo("FRESH");
        assertThat(detail.path("usage").path("visibleObjectCount").asText()).isEqualTo("1");
        String scope = saved.path("scopes").get(0).path("scopeId").asText();
        mvc.perform(
                        delete("/storage/scopes/" + scope)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isConflict());
    }

    @Test
    void exactKeyAndRangeSurviveOneEncodingAndTokensBindSelection() throws Exception {
        var auth = admin();
        var saved = register(auth);
        String scope = saved.path("scopes").get(0).path("scopeId").asText();
        var list = read("/storage/scopes/" + scope + "/objects", auth);
        assertThat(list.path("items").get(0).path("objectKey").asText()).isEqualTo(objectKey);
        var objectResult =
                mvc.perform(
                                get("/storage/scopes/" + scope + "/object")
                                        .session(auth.session())
                                        .param("key", objectKey))
                        .andExpect(status().isOk())
                        .andReturn();
        var object = json.readTree(objectResult.getResponse().getContentAsByteArray()).path("data");
        String token = object.path("objectToken").asText();
        var content =
                mvc.perform(
                                get("/storage/scopes/" + scope + "/object-content")
                                        .session(auth.session())
                                        .param("key", objectKey)
                                        .param("objectToken", token)
                                        .header("Range", "bytes=1-3"))
                        .andExpect(request().asyncStarted())
                        .andReturn();
        mvc.perform(asyncDispatch(content))
                .andExpect(status().isPartialContent())
                .andExpect(header().string("Content-Range", "bytes 1-3/6"))
                .andExpect(content().string("bcd"));
        mvc.perform(
                        get("/storage/scopes/" + scope + "/object-content")
                                .session(auth.session())
                                .param("key", "evidence/other.jpg")
                                .param("objectToken", token))
                .andExpect(status().isConflict());
        assertThat(requests).anyMatch(value -> value.contains("%E4%B8%AD%E6%96%87%2B%2525.jpg"));
    }

    @Test
    void cleanupPreviewIsRealButDeleteAndAutomaticRuleEnableRemainBlocked() throws Exception {
        var auth = admin();
        var saved = register(auth);
        String scope = saved.path("scopes").get(0).path("scopeId").asText();
        var plan =
                write(
                        post("/storage/scopes/" + scope + "/cleanup-plans"),
                        auth,
                        Map.of(
                                "instanceVersion",
                                "0",
                                "scopeVersion",
                                "0",
                                "mode",
                                "FILTER",
                                "relativePrefix",
                                "",
                                "modifiedBefore",
                                "2026-10-01T00:00:00Z",
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        202);
        assertThat(finish(plan, auth).path("status").asText()).isEqualTo("SUCCEEDED");
        String id = plan.path("planId").asText();
        var detail = read("/storage/cleanup-plans/" + id, auth);
        assertThat(detail.path("candidateCount").asText()).isEqualTo("1");
        assertThat(detail.path("deleteReady").asBoolean()).isFalse();
        assertThat(
                        read("/storage/cleanup-plans/" + id + "/items", auth)
                                .path("items")
                                .get(0)
                                .path("status")
                                .asText())
                .isEqualTo("NOT_SENT");
        write(
                post("/storage/cleanup-plans/" + id + "/execute"),
                auth,
                Map.of(
                        "planVersion",
                        detail.path("version").asText(),
                        "confirmationToken",
                        detail.path("confirmationToken").asText(),
                        "idempotencyKey",
                        UUID.randomUUID().toString()),
                409);
        assertThat(requests)
                .noneMatch(value -> value.startsWith("DELETE") || value.startsWith("PUT"));
    }

    @Test
    void cancelledQueuedTaskIsNotClaimedAndAnonymousCannotBrowse() throws Exception {
        var auth = admin();
        var saved = register(auth);
        String id = saved.path("instanceId").asText();
        var receipt =
                write(
                        post("/storage/instances/" + id + "/usage-scans"),
                        auth,
                        Map.of("version", "0", "idempotencyKey", UUID.randomUUID().toString()),
                        202);
        assertThat(
                        write(
                                        post(
                                                "/storage/tasks/"
                                                        + receipt.path("taskId").asText()
                                                        + "/cancel"),
                                        auth,
                                        Map.of("version", receipt.path("version").asText()),
                                        202)
                                .path("status")
                                .asText())
                .isEqualTo("CANCELLED");
        assertThat(tasks.claim("test")).isNull();
        mvc.perform(get("/storage/instances/page")).andExpect(status().isUnauthorized());
    }

    @Test
    void addedScopeAndRuleRetryReturnTheSameReceiptAndRulesCannotBeEnabled() throws Exception {
        var auth = admin();
        var saved = register(auth);
        String instance = saved.path("instanceId").asText();
        var scopeBody =
                Map.of(
                        "instanceVersion",
                        "0",
                        "bucket",
                        "other-bucket",
                        "prefix",
                        "",
                        "idempotencyKey",
                        UUID.randomUUID().toString());
        var added = write(post("/storage/instances/" + instance + "/scopes"), auth, scopeBody, 201);
        assertThat(write(post("/storage/instances/" + instance + "/scopes"), auth, scopeBody, 201))
                .isEqualTo(added);
        String scope = saved.path("scopes").get(0).path("scopeId").asText();
        var ruleBody =
                new LinkedHashMap<String, Object>(
                        Map.of(
                                "name",
                                "Retain seven days",
                                "relativePrefix",
                                "",
                                "retentionDays",
                                7,
                                "scheduleTime",
                                "02:00",
                                "maxDeleteObjects",
                                1000,
                                "maxDeleteBytes",
                                "10737418240",
                                "idempotencyKey",
                                UUID.randomUUID().toString()));
        var rule = write(post("/storage/scopes/" + scope + "/cleanup-rules"), auth, ruleBody, 201);
        assertThat(
                        write(
                                        post("/storage/scopes/" + scope + "/cleanup-rules"),
                                        auth,
                                        ruleBody,
                                        201)
                                .path("id"))
                .isEqualTo(rule.path("id"));
        write(
                put("/storage/cleanup-rules/" + rule.path("id").asText() + "/enabled"),
                auth,
                Map.of("version", "0", "enabled", true),
                409);
        ruleBody.put("scheduleTime", "24:00");
        ruleBody.put("idempotencyKey", UUID.randomUUID().toString());
        write(post("/storage/scopes/" + scope + "/cleanup-rules"), auth, ruleBody, 400);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_cleanup_rule", Integer.class))
                .isOne();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE target_type='STORAGE_SCOPE' AND action='CREATE'",
                                Integer.class))
                .isOne();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE target_type='STORAGE_CLEANUP_RULE' AND action='CREATE'",
                                Integer.class))
                .isOne();
    }

    @Test
    void permissionRevocationStopsQueuedWorkBeforeAnyStorageRequest() throws Exception {
        var auth = admin();
        var saved = register(auth);
        String instance = saved.path("instanceId").asText();
        var receipt =
                write(
                        post("/storage/instances/" + instance + "/usage-scans"),
                        auth,
                        Map.of("version", "0", "idempotencyKey", UUID.randomUUID().toString()),
                        202);
        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=1 AND menu_id=1013");
        var claimed = tasks.claim("test-owner");
        worker.execute(claimed);
        assertThat(tasks.require(receipt.path("taskId").asLong()).getStatus()).isEqualTo("FAILED");
        assertThat(requests).isEmpty();
        mvc.perform(get("/storage/instances/page").session(auth.session()))
                .andExpect(status().isForbidden());
    }

    @Test
    void onlyFreshEmptyObservationsPermitUnregisterAndHistoricalMetadataCanExpire()
            throws Exception {
        empty = true;
        var auth = admin();
        var saved = register(auth);
        String instance = saved.path("instanceId").asText();
        String scope = saved.path("scopes").get(0).path("scopeId").asText();
        var receipt =
                write(
                        post("/storage/instances/" + instance + "/checks"),
                        auth,
                        Map.of("version", "0", "idempotencyKey", UUID.randomUUID().toString()),
                        202);
        finish(receipt, auth);
        mvc.perform(
                        delete("/storage/scopes/" + scope)
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("If-Match", "\"0\""))
                .andExpect(status().isOk());
        jdbc.update(
                "UPDATE storage_task SET finished_at=? WHERE id=?",
                java.sql.Timestamp.valueOf(StorageRules.now().minusDays(31)),
                receipt.path("taskId").asLong());
        enableHistory(30);
        history.prune();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM storage_task WHERE id=?",
                                Integer.class,
                                receipt.path("taskId").asLong()))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_instance", Integer.class))
                .isOne();
        assertThat(requests)
                .noneMatch(value -> value.startsWith("DELETE") || value.startsWith("PUT"));
    }

    @Test
    void historyPolicyIsVisibleAndQueuedCancellationGetsAnExpiryClock() throws Exception {
        var auth = admin();
        var saved = register(auth);
        String instance = saved.path("instanceId").asText();
        mvc.perform(
                        get("/storage/instances/" + instance + "/history-policy")
                                .session(auth.session()))
                .andExpect(status().isOk())
                .andExpect(jsonPath("$.data.inheritGlobal").value(true))
                .andExpect(jsonPath("$.data.effective.enabled").value(false))
                .andExpect(jsonPath("$.data.retentionDays").isEmpty());
        var receipt =
                write(
                        post("/storage/instances/" + instance + "/usage-scans"),
                        auth,
                        Map.of("version", "0", "idempotencyKey", UUID.randomUUID().toString()),
                        202);
        var cancelled =
                write(
                        post("/storage/tasks/" + receipt.path("taskId").asText() + "/cancel"),
                        auth,
                        Map.of("version", receipt.path("version").asText()),
                        202);
        assertThat(cancelled.path("status").asText()).isEqualTo("CANCELLED");
        assertThat(cancelled.path("finishedAt").isNull()).isFalse();
        assertThat(requests).isEmpty();
    }

    private void historicalTask(long id, String status, int ageDays) {
        var old = java.sql.Timestamp.valueOf(StorageRules.now().minusDays(ageDays));
        jdbc.update(
                "INSERT INTO storage_task(id,run_id,instance_id,actor_id,type,status,phase,request_key,request_fingerprint,created_at,finished_at) VALUES(?,?,1,1,'CHECK',?,'FINISHED',?,'fixture',?,?)",
                id,
                id,
                status,
                UUID.randomUUID().toString(),
                old,
                old);
    }

    @Test
    void historyRetentionUsesFinishTimeAndPreservesActiveAndRecentWork() {
        enableHistory(7);
        historicalTask(7101, "SUCCEEDED", 8);
        historicalTask(7102, "FAILED", 8);
        historicalTask(7103, "CANCELLED", 8);
        historicalTask(7104, "SUCCEEDED", 6);
        historicalTask(7105, "RUNNING", 40);
        historicalTask(7106, "QUEUED", 40);
        historicalTask(7107, "PAUSED_LIMIT", 40);
        historicalTask(7108, "IN_PROGRESS", 40);
        historicalTask(7109, "RECONCILING", 40);
        history.prune();
        assertThat(jdbc.queryForList("SELECT id FROM storage_task ORDER BY id", Long.class))
                .containsExactly(7104L, 7105L, 7106L, 7107L, 7108L, 7109L);
        assertThat(requests).isEmpty();
    }

    @Test
    void historyRetainsExecutionSnapshotsAndThenRemovesAssociatedMetadata() {
        enableHistory(30);
        historicalTask(7201, "SUCCEEDED", 40);
        historicalTask(7202, "RUNNING", 40);
        var old = java.sql.Timestamp.valueOf(StorageRules.now().minusDays(40));
        jdbc.update(
                "INSERT INTO storage_cleanup_plan(id,instance_id,scope_id,actor_id,instance_version,scope_version,mode,status,execution_task_id,created_at,expires_at) VALUES(7201,1,1,1,0,0,'FOLDER','RUNNING',7202,?,?)",
                old,
                old);
        jdbc.update(
                "INSERT INTO storage_cleanup_item(id,plan_id,object_key,size_bytes,etag,last_modified,status) VALUES(7203,7201,'fixture.txt',1,'fixture',?,'NOT_SENT')",
                old);
        history.prune();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_task", Integer.class))
                .isEqualTo(2);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_cleanup_item", Integer.class))
                .isOne();
        jdbc.update("UPDATE storage_task SET status='SUCCEEDED' WHERE id=7202");
        history.prune();
        history.prune();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_task", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_cleanup_plan", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_cleanup_item", Integer.class))
                .isZero();
        assertThat(requests).isEmpty();
    }

    @Test
    void historyRunsInBoundedBatches() {
        enableHistory(30);
        for (int index = 0; index < 103; index++) historicalTask(7300 + index, "SUCCEEDED", 40);
        history.prune();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_task", Integer.class))
                .isEqualTo(3);
        history.prune();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_task", Integer.class))
                .isZero();
        assertThat(requests).isEmpty();
    }

    private void enableHistory(int days) {
        jdbc.update(
                "UPDATE storage_history_policy SET enabled=1, retention_days=? WHERE id=0", days);
    }

    @Test
    void historyIsDisabledUntilExplicitlyEnabled() {
        historicalTask(7501, "SUCCEEDED", 400);
        history.prune();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM storage_task", Integer.class)).isOne();
        assertThat(requests).isEmpty();
    }

    private Map<String, Object> policy(
            String version, boolean inherit, boolean enabled, Integer days) {
        var body = new LinkedHashMap<String, Object>();
        body.put("version", version);
        body.put("inheritGlobal", inherit);
        body.put("enabled", enabled);
        body.put("retentionDays", days);
        return body;
    }

    @Test
    void globalAndInstancePoliciesPersistValidateAndProtectVersions() throws Exception {
        var auth = admin();
        var instance = register(auth);
        String own =
                "/storage/instances/" + instance.path("instanceId").asText() + "/history-policy";
        assertThat(read("/storage/history-policy", auth).path("enabled").asBoolean()).isFalse();
        write(put("/storage/history-policy"), auth, policy("0", false, true, null), 400);
        write(put("/storage/history-policy"), auth, policy("0", false, true, 0), 400);
        write(put("/storage/history-policy"), auth, policy("0", true, false, null), 400);
        write(put("/storage/history-policy"), auth, policy("0", false, true, 90), 200);
        assertThat(read(own, auth).path("effective").path("retentionDays").asInt()).isEqualTo(90);
        write(put("/storage/history-policy"), auth, policy("0", false, false, null), 409);
        write(put(own), auth, policy("0", false, true, 7), 200);
        assertThat(read(own, auth).path("effective").path("source").asText()).isEqualTo("INSTANCE");
        assertThat(read(own, auth).path("effective").path("retentionDays").asInt()).isEqualTo(7);
        write(put(own), auth, policy("1", false, false, null), 200);
        assertThat(read(own, auth).path("effective").path("enabled").asBoolean()).isFalse();
        write(put(own), auth, policy("2", true, false, null), 200);
        assertThat(read(own, auth).path("effective").path("retentionDays").asInt()).isEqualTo(90);
        write(put("/storage/history-policy"), auth, policy("1", false, false, null), 200);
        assertThat(read(own, auth).path("effective").path("enabled").asBoolean()).isFalse();
        mvc.perform(get("/storage/history-policy")).andExpect(status().isUnauthorized());
        assertThat(requests).isEmpty();
    }

    @Test
    void globalCleanupRespectsOverridesAndDoesNotChangeCurrentSnapshots() throws Exception {
        var auth = admin();
        var first = register(auth);
        var body = input();
        body.put("name", "Second storage");
        var second = write(post("/storage/instances"), auth, body, 201);
        var firstId = first.path("instanceId").asText();
        var secondId = second.path("instanceId").asText();
        write(put("/storage/history-policy"), auth, policy("0", false, true, 30), 200);
        write(
                put("/storage/instances/" + secondId + "/history-policy"),
                auth,
                policy("0", false, true, 90),
                200);
        historicalTask(7601, "SUCCEEDED", 40);
        historicalTask(7602, "SUCCEEDED", 40);
        jdbc.update("UPDATE storage_task SET instance_id=? WHERE id=7601", firstId);
        jdbc.update("UPDATE storage_task SET instance_id=? WHERE id=7602", secondId);
        var snapshot = read("/storage/instances/" + firstId, auth);
        history.prune();
        assertThat(
                        jdbc.queryForList(
                                "SELECT id FROM storage_task WHERE id IN (7601,7602) ORDER BY id",
                                Long.class))
                .containsExactly(7602L);
        var after = read("/storage/instances/" + firstId, auth);
        assertThat(after.path("usage")).isEqualTo(snapshot.path("usage"));
        assertThat(after.path("observation")).isEqualTo(snapshot.path("observation"));
        write(put("/storage/history-policy"), auth, policy("1", false, false, null), 200);
        write(
                put("/storage/instances/" + secondId + "/history-policy"),
                auth,
                policy("1", false, true, 7),
                200);
        history.prune();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM storage_task WHERE id IN (7601,7602)",
                                Integer.class))
                .isZero();
        assertThat(requests).isEmpty();
    }

    @Test
    void concurrentSubmissionsCannotOversubscribeTheGlobalQueue() throws Exception {
        var auth = admin();
        var ids = new ArrayList<String>();
        for (int index = 0; index < 21; index++) {
            var body = input();
            body.put("name", "Queue fixture " + index);
            ids.add(write(post("/storage/instances"), auth, body, 201).path("instanceId").asText());
        }
        for (int index = 0; index < 19; index++)
            write(
                    post("/storage/instances/" + ids.get(index) + "/usage-scans"),
                    auth,
                    Map.of("version", "0", "idempotencyKey", UUID.randomUUID().toString()),
                    202);
        var outcomes = new ArrayList<java.util.concurrent.CompletableFuture<Integer>>();
        for (String id : ids.subList(19, 21))
            outcomes.add(
                    java.util.concurrent.CompletableFuture.supplyAsync(
                            () -> {
                                try {
                                    return mvc.perform(
                                                    post("/storage/instances/"
                                                                    + id
                                                                    + "/usage-scans")
                                                            .session(auth.session())
                                                            .header(auth.header(), auth.token())
                                                            .contentType("application/json")
                                                            .content(
                                                                    json.writeValueAsBytes(
                                                                            Map.of(
                                                                                    "version",
                                                                                    "0",
                                                                                    "idempotencyKey",
                                                                                    UUID.randomUUID()
                                                                                            .toString()))))
                                            .andReturn()
                                            .getResponse()
                                            .getStatus();
                                } catch (Exception ex) {
                                    throw new java.util.concurrent.CompletionException(ex);
                                }
                            }));
        assertThat(outcomes.stream().map(future -> future.join()).toList())
                .containsExactlyInAnyOrder(202, 429);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM storage_task WHERE status='QUEUED'",
                                Integer.class))
                .isEqualTo(20);
        assertThat(requests).isEmpty();
    }
}
