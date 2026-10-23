package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.when;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.storage.s3.*;
import com.streamfusion.platform.support.CameraTestSupport;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import org.junit.jupiter.api.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.mock.web.MockMultipartFile;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/**
 * HTTP fixtures plus isolated maintained SQL exercise real multipart, signing and conditional
 * writes.
 */
class StorageFileManagementTest extends CameraTestSupport {
    @MockitoBean StorageNetwork network;
    @Autowired StorageTaskService taskService;
    private HttpServer server;
    private final Map<String, byte[]> stored = new ConcurrentHashMap<>();
    private final List<String> requests = new CopyOnWriteArrayList<>();
    private volatile boolean replaceAtDelete;

    @BeforeEach
    void startStorage() throws Exception {
        when(network.resolve(any(), anyString())).thenReturn(InetAddress.getLoopbackAddress());
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/",
                exchange -> {
                    String path = exchange.getRequestURI().getPath(),
                            method = exchange.getRequestMethod();
                    requests.add(method + " " + path);
                    assertThat(exchange.getRequestHeaders().getFirst("Authorization"))
                            .startsWith("AWS4-HMAC-SHA256 Credential=fixture-access/");
                    int code;
                    byte[] response = new byte[0];
                    if (method.equals("PUT")) {
                        byte[] body = exchange.getRequestBody().readAllBytes();
                        assertThat(exchange.getRequestHeaders().getFirst("If-None-Match"))
                                .isEqualTo("*");
                        assertThat(exchange.getRequestHeaders().getFirst("x-amz-content-sha256"))
                                .isEqualTo(S3Signature.hash(body));
                        code = stored.putIfAbsent(path, body) == null ? 200 : 412;
                    } else if (method.equals("DELETE")) {
                        if (replaceAtDelete)
                            stored.put(path, "replacement".getBytes(StandardCharsets.UTF_8));
                        byte[] current = stored.get(path);
                        if (current != null
                                && !etag(current)
                                        .equals(exchange.getRequestHeaders().getFirst("If-Match")))
                            code = 412;
                        else {
                            stored.remove(path);
                            code = 204;
                        }
                    } else {
                        byte[] current = stored.get(path);
                        code = current == null ? 404 : 200;
                        if (current != null) {
                            exchange.getResponseHeaders().set("ETag", etag(current));
                            exchange.getResponseHeaders()
                                    .set("Last-Modified", "Thu, 01 Jan 2026 00:00:00 GMT");
                            exchange.getResponseHeaders().set("Content-Type", "image/png");
                            exchange.getResponseHeaders()
                                    .set("Content-Length", Integer.toString(current.length));
                            response = current;
                        }
                    }
                    if (method.equals("HEAD") || response.length == 0)
                        exchange.sendResponseHeaders(code, -1);
                    else {
                        exchange.sendResponseHeaders(code, response.length);
                        exchange.getResponseBody().write(response);
                    }
                    exchange.close();
                });
        server.start();
    }

    @AfterEach
    void stopStorage() {
        if (server != null) server.stop(0);
    }

    private static String etag(byte[] body) {
        return "\"" + S3Signature.hash(body) + "\"";
    }

    private String register(Csrf auth) throws Exception {
        var body =
                Map.of(
                        "name",
                        "File fixture",
                        "endpoint",
                        "http://127.0.0.1:" + server.getAddress().getPort(),
                        "accessKey",
                        Map.of("action", "REPLACE", "value", "fixture-access"),
                        "secretKey",
                        Map.of("action", "REPLACE", "value", "fixture-secret"),
                        "scopes",
                        List.of(Map.of("bucket", "fixture-bucket", "prefix", "images/")),
                        "idempotencyKey",
                        UUID.randomUUID().toString());
        return write(post("/storage/instances"), auth, body, 201)
                .path("scopes")
                .get(0)
                .path("scopeId")
                .asText();
    }

    private JsonNode upload(
            Csrf auth,
            String scope,
            String name,
            byte[] body,
            String request,
            String version,
            int expected)
            throws Exception {
        var response =
                mvc.perform(
                                multipart("/storage/scopes/" + scope + "/objects/upload")
                                        .file(
                                                new MockMultipartFile(
                                                        "file", name, "image/png", body))
                                        .param("instanceVersion", version)
                                        .param("scopeVersion", "0")
                                        .param("relativePrefix", "")
                                        .param("idempotencyKey", request)
                                        .session(auth.session())
                                        .header(auth.header(), auth.token()))
                        .andExpect(status().is(expected))
                        .andReturn()
                        .getResponse();
        return json.readTree(response.getContentAsByteArray()).path("data");
    }

    private Map<String, Object> deletion(String key, String token, String request) {
        return Map.of(
                "key",
                key,
                "objectToken",
                token,
                "instanceVersion",
                "0",
                "scopeVersion",
                "0",
                "idempotencyKey",
                request);
    }

    @Test
    void uploadIsScopedSignedIdempotentAndNeverOverwritesExistingNames() throws Exception {
        var auth = admin();
        String scope = register(auth);
        String request = UUID.randomUUID().toString();
        byte[] content = "image-fixture".getBytes(StandardCharsets.UTF_8);
        jdbc.update("UPDATE storage_scope SET observation='{}'");
        jdbc.update(
                "UPDATE storage_instance SET usage_snapshot='{\"complete\":true,\"logicalBytes\":\"0\"}'");
        var first = upload(auth, scope, "中文+%25.png", content, request, "0", 201);
        var replay = upload(auth, scope, "中文+%25.png", content, request, "0", 201);
        assertThat(replay.path("taskId")).isEqualTo(first.path("taskId"));
        assertThat(stored.get("/fixture-bucket/images/中文+%25.png")).isEqualTo(content);
        assertThat(requests.stream().filter(r -> r.startsWith("PUT")).count()).isOne();
        upload(
                auth,
                scope,
                "中文+%25.png",
                "different".getBytes(StandardCharsets.UTF_8),
                UUID.randomUUID().toString(),
                "0",
                409);
        assertThat(stored.get("/fixture-bucket/images/中文+%25.png")).isEqualTo(content);
        assertThat(jdbc.queryForObject("SELECT observation FROM storage_scope", String.class))
                .isNull();
        assertThat(jdbc.queryForObject("SELECT usage_snapshot FROM storage_instance", String.class))
                .contains("\"stale\":true");
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE target_type='STORAGE_OBJECT'",
                                Integer.class))
                .isOne();
    }

    @Test
    void staleVersionsUnsafePathsLargeFilesAndMissingCsrfNeverReachStorage() throws Exception {
        var auth = admin();
        String scope = register(auth);
        upload(auth, scope, "ok.png", new byte[1], UUID.randomUUID().toString(), "9", 409);
        upload(auth, scope, "../escape.png", new byte[1], UUID.randomUUID().toString(), "0", 400);
        upload(
                auth,
                scope,
                "huge.png",
                new byte[StorageFileService.MAX_UPLOAD_BYTES + 1],
                UUID.randomUUID().toString(),
                "0",
                413);
        mvc.perform(
                        multipart("/storage/scopes/" + scope + "/objects/upload")
                                .file(
                                        new MockMultipartFile(
                                                "file", "x.png", "image/png", new byte[1]))
                                .session(auth.session()))
                .andExpect(status().isForbidden());
        assertThat(requests).isEmpty();
    }

    @Test
    void singleFileDeleteUsesTheSelectionAndConditionalHeaderAndDoesNotReplay() throws Exception {
        var auth = admin();
        String scope = register(auth);
        String key = "images/delete.png";
        stored.put("/fixture-bucket/" + key, "original".getBytes(StandardCharsets.UTF_8));
        var metadata = read("/storage/scopes/" + scope + "/object?key=" + key, auth);
        var command =
                deletion(key, metadata.path("objectToken").asText(), UUID.randomUUID().toString());
        var receipt = write(delete("/storage/scopes/" + scope + "/object"), auth, command, 200);
        assertThat(stored).doesNotContainKey("/fixture-bucket/" + key);
        assertThat(
                        write(delete("/storage/scopes/" + scope + "/object"), auth, command, 200)
                                .path("taskId"))
                .isEqualTo(receipt.path("taskId"));
        assertThat(requests.stream().filter(r -> r.startsWith("DELETE")).count()).isOne();
        assertThat(
                        read(
                                        "/storage/instances/"
                                                + receipt.path("instanceId").asText()
                                                + "/tasks/page",
                                        auth)
                                .path("items")
                                .get(0)
                                .path("type")
                                .asText())
                .isEqualTo("FILE_DELETE");
    }

    @Test
    void anOverwriteAtDeleteTimeIsRejectedAndTheReplacementRemains() throws Exception {
        var auth = admin();
        String scope = register(auth);
        String key = "images/race.png";
        stored.put("/fixture-bucket/" + key, "original".getBytes(StandardCharsets.UTF_8));
        var metadata = read("/storage/scopes/" + scope + "/object?key=" + key, auth);
        replaceAtDelete = true;
        write(
                delete("/storage/scopes/" + scope + "/object"),
                auth,
                deletion(key, metadata.path("objectToken").asText(), UUID.randomUUID().toString()),
                409);
        assertThat(stored.get("/fixture-bucket/" + key))
                .isEqualTo("replacement".getBytes(StandardCharsets.UTF_8));
        assertThat(
                        jdbc.queryForObject(
                                "SELECT status FROM storage_task WHERE type='FILE_DELETE'",
                                String.class))
                .isEqualTo("FAILED");
    }

    @Test
    void foldersAreCreatedWithoutRecursiveDeleteAndAbandonedMutationsAreNotRequeued()
            throws Exception {
        var auth = admin();
        String scope = register(auth);
        var folder =
                write(
                        post("/storage/scopes/" + scope + "/objects/folder"),
                        auth,
                        Map.of(
                                "name",
                                "中文文件夹",
                                "relativePrefix",
                                "",
                                "instanceVersion",
                                "0",
                                "scopeVersion",
                                "0",
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        201);
        assertThat(stored).containsKey("/fixture-bucket/images/中文文件夹/");
        write(
                delete("/storage/scopes/" + scope + "/object"),
                auth,
                deletion("images/中文文件夹/", "unused", UUID.randomUUID().toString()),
                400);
        jdbc.update(
                "UPDATE storage_task SET status='IN_PROGRESS',lease_until=? WHERE id=?",
                StorageRules.now().minusMinutes(1),
                folder.path("taskId").asLong());
        taskService.recover();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT status FROM storage_task WHERE id=?",
                                String.class,
                                folder.path("taskId").asLong()))
                .isEqualTo("UNKNOWN");
        assertThat(taskService.claim("never-replay-writes")).isNull();
        assertThat(requests).noneMatch(r -> r.startsWith("DELETE"));
    }
}
