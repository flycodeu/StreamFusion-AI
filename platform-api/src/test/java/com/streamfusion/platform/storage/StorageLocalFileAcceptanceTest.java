package com.streamfusion.platform.storage;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.storage.mapper.StorageInstanceMapper;
import com.streamfusion.platform.storage.s3.StorageRemoteException;
import com.streamfusion.platform.storage.s3.StorageS3Client;
import com.streamfusion.platform.support.CameraTestSupport;
import java.awt.image.BufferedImage;
import java.io.ByteArrayOutputStream;
import java.nio.file.*;
import java.util.*;
import javax.imageio.ImageIO;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.autoconfigure.web.servlet.MockMvcPrint;
import org.springframework.mock.web.MockMultipartFile;

/** Opt-in real local S3, isolated H2/platform session; mutates only a fresh guarded test prefix. */
@EnabledIfEnvironmentVariable(named = "SF_STORAGE_LOCAL_ACCEPTANCE", matches = "true")
@AutoConfigureMockMvc(print = MockMvcPrint.NONE, printOnlyOnFailure = false)
class StorageLocalFileAcceptanceTest extends CameraTestSupport {
    @Autowired StorageS3Client s3;
    @Autowired StorageInstanceMapper instances;

    @Test
    void localSeaweedHandlesMultipartNoOverwriteFolderDownloadAndConditionalDelete()
            throws Exception {
        String prefix = "_storage_file_fix_10_" + UUID.randomUUID() + "/";
        assertThat(prefix).matches("_storage_file_fix_10_[0-9a-f-]{36}/");
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
        var image = new BufferedImage(64, 32, BufferedImage.TYPE_INT_RGB);
        var output = new ByteArrayOutputStream();
        ImageIO.write(image, "png", output);
        byte[] body = output.toByteArray();
        write(
                post("/storage/scopes/" + scope + "/objects/folder"),
                auth,
                Map.of(
                        "instanceVersion",
                        "0",
                        "scopeVersion",
                        "0",
                        "relativePrefix",
                        "",
                        "name",
                        "中文文件夹",
                        "idempotencyKey",
                        UUID.randomUUID().toString()),
                201);
        String name = "平台上传验证+%25.png";
        var first = upload(auth, scope, name, body, 201);
        upload(
                auth,
                scope,
                name,
                "replacement".getBytes(java.nio.charset.StandardCharsets.UTF_8),
                409);
        String key = prefix + "中文文件夹/" + name;
        var objectRequest =
                get("/storage/scopes/" + scope + "/object")
                        .param("key", key)
                        .session(auth.session());
        var metadataResponse =
                mvc.perform(objectRequest).andExpect(status().isOk()).andReturn().getResponse();
        JsonNode metadata = json.readTree(metadataResponse.getContentAsByteArray()).path("data");
        assertThat(metadata.path("sizeBytes").asInt()).isEqualTo(body.length);
        var stream =
                mvc.perform(
                                get("/storage/scopes/" + scope + "/object-content")
                                        .param("key", key)
                                        .param("objectToken", metadata.path("objectToken").asText())
                                        .param("disposition", "attachment")
                                        .session(auth.session()))
                        .andExpect(request().asyncStarted())
                        .andReturn();
        mvc.perform(asyncDispatch(stream))
                .andExpect(status().isOk())
                .andExpect(content().bytes(body));
        assertThat(key).startsWith(prefix);
        var deleted =
                write(
                        delete("/storage/scopes/" + scope + "/object"),
                        auth,
                        Map.of(
                                "key",
                                key,
                                "objectToken",
                                metadata.path("objectToken").asText(),
                                "instanceVersion",
                                "0",
                                "scopeVersion",
                                "0",
                                "idempotencyKey",
                                UUID.randomUUID().toString()),
                        200);
        mvc.perform(
                        get("/storage/scopes/" + scope + "/object")
                                .param("key", key)
                                .session(auth.session()))
                .andExpect(status().isNotFound());
        var instance = instances.selectById(registered.path("instanceId").asLong());
        for (String directory : List.of(prefix + "中文文件夹/", prefix)) {
            try {
                s3.deleteEmptyMarker(
                        instance,
                        "streamfusion-evidence",
                        s3.head(instance, "streamfusion-evidence", directory));
            } catch (StorageRemoteException absent) {
                if (absent.status() != 404) throw absent;
            }
        }
        var result =
                Map.of(
                        "status",
                        "PASS",
                        "prefix",
                        prefix,
                        "fileBytes",
                        body.length,
                        "upload",
                        first.path("status").asText(),
                        "sameNameUpload",
                        409,
                        "downloadMatches",
                        true,
                        "delete",
                        deleted.path("status").asText(),
                        "headAfterDelete",
                        404,
                        "database",
                        "isolated H2",
                        "existingFilesTouched",
                        false);
        Files.createDirectories(Path.of(".run/storage-fix-10"));
        Files.writeString(
                Path.of(".run/storage-fix-10/live-file-result.json"),
                json.writerWithDefaultPrettyPrinter().writeValueAsString(result));
    }

    private JsonNode upload(Csrf auth, String scope, String name, byte[] body, int expected)
            throws Exception {
        var response =
                mvc.perform(
                                multipart("/storage/scopes/" + scope + "/objects/upload")
                                        .file(
                                                new MockMultipartFile(
                                                        "file", name, "image/png", body))
                                        .param("instanceVersion", "0")
                                        .param("scopeVersion", "0")
                                        .param("relativePrefix", "中文文件夹/")
                                        .param("idempotencyKey", UUID.randomUUID().toString())
                                        .session(auth.session())
                                        .header(auth.header(), auth.token()))
                        .andExpect(status().is(expected))
                        .andReturn()
                        .getResponse();
        return json.readTree(response.getContentAsByteArray()).path("data");
    }
}
