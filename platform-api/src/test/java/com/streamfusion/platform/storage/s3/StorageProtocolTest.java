package com.streamfusion.platform.storage.s3;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.net.*;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.Map;
import java.util.concurrent.*;
import org.junit.jupiter.api.Test;

class StorageProtocolTest {
    @Test
    void inspectionPresignedPutMatchesBotocoreAndBindsRequiredHeaders() {
        var url =
                S3Signature.presignedUrl(
                        "https://storage.example.com",
                        "test-bucket",
                        "inspections/r1/e1/1.jpg",
                        "us-east-1",
                        "AKID",
                        "SECRET",
                        600,
                        Map.of(
                                "if-none-match",
                                "*",
                                "content-length",
                                "5",
                                "content-type",
                                "image/jpeg",
                                "x-amz-checksum-sha256",
                                "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="),
                        Instant.parse("2026-10-28T00:00:00Z"));
        assertThat(url)
                .contains(
                        "X-Amz-SignedHeaders=content-length%3Bcontent-type%3Bhost%3Bif-none-match%3Bx-amz-checksum-sha256")
                .contains(
                        "X-Amz-Signature=fdcff90e5ba8a82931529ff63c149d036096fcd7e449891511ffd6c883964f2a");
    }

    @Test
    void awsS3GetRangeExampleMatchesIndependentlyCalculatedSignature() {
        var signed =
                S3Signature.sign(
                        "GET",
                        "/test.txt",
                        "",
                        "examplebucket.s3.amazonaws.com",
                        "us-east-1",
                        "AKIAIOSFODNN7EXAMPLE",
                        "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
                        new byte[0],
                        Map.of("range", "bytes=0-9"),
                        Instant.parse("2013-05-24T00:00:00Z"));
        assertThat(signed.get("authorization"))
                .endsWith(
                        "Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41");
    }

    @Test
    void encodingPreservesExactS3KeyBytesWithoutFilesystemNormalization() {
        assertThat(S3Signature.encode("图/a+b %25//c", true))
                .isEqualTo("%E5%9B%BE/a%2Bb%20%2525//c");
        assertThat(S3Signature.query(Map.of("prefix", "图/", "continuation-token", "+/=")))
                .isEqualTo("continuation-token=%2B%2F%3D&prefix=%E5%9B%BE%2F");
    }

    @Test
    void externalEntitiesAndMalformedXmlAreRejected() {
        for (String xml :
                new String[] {
                    "<!DOCTYPE x [<!ENTITY secret SYSTEM 'file:///not-to-be-read'>]><x>&secret;</x>",
                    "<invalid>"
                })
            assertThatThrownBy(() -> StorageS3Client.xml(xml.getBytes(StandardCharsets.UTF_8)))
                    .isInstanceOf(StorageRemoteException.class)
                    .hasMessage("STORAGE_XML_INVALID");
    }

    @Test
    void chunkedResponsesAreBoundedAndDuplicateLengthOrPrematureEofAreRejected() throws Exception {
        assertThat(
                        exchange(
                                "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n",
                                10))
                .isEqualTo("abc");
        assertThatThrownBy(
                        () ->
                                exchange(
                                        "HTTP/1.1 200 OK\r\nContent-Length: 3\r\nContent-Length: 3\r\n\r\nabc",
                                        10))
                .isInstanceOf(StorageRemoteException.class);
        assertThatThrownBy(() -> exchange("HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nabc", 10))
                .isInstanceOf(StorageRemoteException.class);
        assertThatThrownBy(() -> exchange("HTTP/1.1 200 OK\r\nContent-Length: 6\r\n\r\nabcdef", 3))
                .isInstanceOf(StorageRemoteException.class);
    }

    private String exchange(String wire, int limit) throws Exception {
        var network = mock(StorageNetwork.class);
        when(network.resolve(any(), anyString())).thenReturn(InetAddress.getLoopbackAddress());
        try (var server = new ServerSocket(0, 1, InetAddress.getLoopbackAddress())) {
            var serving =
                    CompletableFuture.runAsync(
                            () -> {
                                try (var socket = server.accept()) {
                                    var reader =
                                            new java.io.BufferedReader(
                                                    new java.io.InputStreamReader(
                                                            socket.getInputStream()));
                                    for (String line;
                                            (line = reader.readLine()) != null
                                                    && !line.isEmpty(); ) {}
                                    socket.getOutputStream()
                                            .write(wire.getBytes(StandardCharsets.ISO_8859_1));
                                } catch (Exception ex) {
                                    throw new CompletionException(ex);
                                }
                            });
            var transport = new StorageHttpTransport(network);
            try (var response =
                    transport.exchange(
                            URI.create("http://10.0.1.5:" + server.getLocalPort()),
                            "test",
                            "GET",
                            "/bucket",
                            Map.of("host", "10.0.1.5"),
                            new byte[0],
                            false)) {
                return new String(response.bytes(limit), StandardCharsets.UTF_8);
            } finally {
                transport.close();
                serving.get(3, TimeUnit.SECONDS);
            }
        }
    }
}
