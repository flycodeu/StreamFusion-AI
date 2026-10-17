package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;

class ZlmDirectRealMediaTest {
    @Test
    @EnabledIfEnvironmentVariable(named = "SF_PREVIEW_DIRECT_REAL_MEDIA_TEST", matches = "true")
    void isolatedZlmPullsH264CreatesViewerAndReleasesBothExactStreams() throws Exception {
        int apiPort = Integer.parseInt(System.getenv("SF_PREVIEW_DIRECT_ZLM_API_PORT"));
        int rtspPort = Integer.parseInt(System.getenv("SF_PREVIEW_DIRECT_ZLM_RTSP_PORT"));
        Path secret = Path.of(System.getenv("SF_PREVIEW_DIRECT_ZLM_SECRET_FILE"));
        var gateway = new ZlmDirectGateway(new ObjectMapper());
        var connection =
                new ZlmDirectConnection(
                        "zlm-1234567890abcdef",
                        "isolated",
                        URI.create("http://127.0.0.1:" + apiPort),
                        secret,
                        "127.0.0.1");
        String suffix = UUID.randomUUID().toString().replace("-", "");
        var source = new ZlmDirectGateway.Stream("sf_preview_" + suffix.substring(0, 12), suffix);
        var viewer =
                new ZlmDirectGateway.Stream(
                        source.app(), UUID.randomUUID().toString().replace("-", ""));
        gateway.requireReady(connection, PreviewDeadline.after(Duration.ofSeconds(5)));
        assertThat(gateway.version(connection, PreviewDeadline.after(Duration.ofSeconds(5))))
                .isNotBlank();
        try {
            gateway.open(
                    connection,
                    source,
                    URI.create("rtsp://127.0.0.1:" + rtspPort + "/fixture/source"),
                    PreviewDeadline.after(Duration.ofSeconds(10)));
            awaitH264(gateway, connection, source);
            gateway.openViewer(
                    connection, source, viewer, PreviewDeadline.after(Duration.ofSeconds(10)));
            awaitH264(gateway, connection, viewer);
        } finally {
            gateway.close(connection, viewer, PreviewDeadline.after(Duration.ofSeconds(10)));
            gateway.close(connection, source, PreviewDeadline.after(Duration.ofSeconds(10)));
        }
        assertThat(gateway.exists(connection, viewer, PreviewDeadline.after(Duration.ofSeconds(5))))
                .isFalse();
        assertThat(gateway.exists(connection, source, PreviewDeadline.after(Duration.ofSeconds(5))))
                .isFalse();
    }

    private static void awaitH264(
            ZlmDirectGateway gateway,
            ZlmDirectConnection connection,
            ZlmDirectGateway.Stream stream)
            throws InterruptedException {
        for (int attempt = 0; attempt < 20; attempt++) {
            var stats =
                    gateway.stats(connection, stream, PreviewDeadline.after(Duration.ofSeconds(3)));
            if ("READY".equals(stats.state()) && "H264".equals(stats.videoCodec())) return;
            Thread.sleep(200);
        }
        throw new AssertionError("Isolated ZLM did not expose a ready H264 track");
    }
}
