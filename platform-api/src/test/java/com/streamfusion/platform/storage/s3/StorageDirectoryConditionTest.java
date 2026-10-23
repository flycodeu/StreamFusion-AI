package com.streamfusion.platform.storage.s3;

import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.storage.StorageSecrets;
import com.streamfusion.platform.storage.entity.StorageInstance;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import org.junit.jupiter.api.Test;

class StorageDirectoryConditionTest {
    private static final String SYNTHETIC = "\"d41d8cd98f00b204e9800998ecf8427e-0\"";

    @Test
    void anUnmarkedEmptyDirectoryUsesTheConditionalEmptyContentTag() throws Exception {
        verifyCondition("dir/", "application/x-directory", SYNTHETIC, "\"\"");
    }

    @Test
    void anExplicitMarkerKeepsItsReturnedTag() throws Exception {
        verifyCondition("dir/", "application/x-directory", "\"stored-tag\"", "\"stored-tag\"");
    }

    @Test
    void ordinaryFilesAndOtherContentTypesNeverUseTheDirectoryCompatibilityRule() throws Exception {
        verifyCondition("file", "application/octet-stream", SYNTHETIC, SYNTHETIC);
        verifyCondition("dir/", "image/png", SYNTHETIC, SYNTHETIC);
    }

    private void verifyCondition(String key, String type, String tag, String expected)
            throws Exception {
        var transport = mock(StorageHttpTransport.class);
        var secrets = mock(StorageSecrets.class);
        var instance = new StorageInstance();
        instance.setEndpoint("http://127.0.0.1:8333");
        instance.setNetworkPolicyKey("local");
        instance.setRegion("us-east-1");
        when(secrets.read(instance))
                .thenReturn(
                        new ObjectMapper()
                                .readTree("{\"accessKey\":\"fixture\",\"secretKey\":\"fixture\"}"));
        var listing = mock(StorageHttpTransport.Response.class);
        when(listing.status()).thenReturn(200);
        when(listing.bytes(anyInt()))
                .thenReturn(
                        "<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>"
                                .getBytes(StandardCharsets.UTF_8));
        var deleted = mock(StorageHttpTransport.Response.class);
        when(deleted.status()).thenReturn(204);
        when(transport.exchange(
                        any(), anyString(), eq("GET"), anyString(), anyMap(), any(), eq(false)))
                .thenReturn(listing);
        when(transport.exchange(
                        any(), anyString(), eq("DELETE"), anyString(), anyMap(), any(), eq(false)))
                .thenReturn(deleted);
        var client = new StorageS3Client(transport, secrets);
        var object = new StorageS3Client.ObjectInfo(key, 0, tag, Instant.EPOCH, type, null);
        if (key.endsWith("/")) client.deleteEmptyMarker(instance, "bucket", object);
        else client.deleteSelected(instance, "bucket", object);
        verify(transport)
                .exchange(
                        any(),
                        eq("local"),
                        eq("DELETE"),
                        eq("/bucket/" + key),
                        argThat(headers -> expected.equals(headers.get("if-match"))),
                        any(),
                        eq(false));
    }
}
