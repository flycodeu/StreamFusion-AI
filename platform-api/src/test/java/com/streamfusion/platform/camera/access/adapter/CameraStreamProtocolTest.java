package com.streamfusion.platform.camera.access.adapter;

import static org.assertj.core.api.Assertions.*;

import org.junit.jupiter.api.Test;

class CameraStreamProtocolTest {
    @Test
    void protocolsHaveExplicitSchemesAndPorts() {
        assertThat(
                        CameraStreamProtocol.RTMP.port(
                                CameraStreamProtocol.RTMP.parse("rtmp://camera/live/main")))
                .isEqualTo(1935);
        assertThat(
                        CameraStreamProtocol.HLS.port(
                                CameraStreamProtocol.HLS.parse(
                                        "https://camera/live/index.m3u8?token=secret")))
                .isEqualTo(443);
        assertThat(CameraStreamProtocol.FLV.parse("http://camera/live.flv").getPath())
                .isEqualTo("/live.flv");
        assertThat(
                        CameraStreamProtocol.GB28181
                                .parse(
                                        "gb28181://10.0.0.1:554/34020000001320000001/34020000001320000002")
                                .getPort())
                .isEqualTo(554);
        assertThat(
                        CameraStreamProtocol.CRTSP
                                .parse(
                                        "rtsp://10.0.0.1/sf_ingress/0123456789abcdef0123456789abcdef")
                                .getScheme())
                .isEqualTo("rtsp");
    }

    @Test
    void wrongSchemesAndInvalidPassiveSourcesAreRejected() {
        for (String raw :
                new String[] {
                    "http://camera/live.flv",
                    "rtmps://camera/live/main",
                    "rtmp://camera:0/live/main",
                    "rtmp://camera/live/main#frag",
                    "rtmp://camera/live/\nmain"
                })
            assertThatThrownBy(() -> CameraStreamProtocol.RTMP.parse(raw))
                    .isInstanceOf(RuntimeException.class);
        assertThatThrownBy(() -> CameraStreamProtocol.HLS.parse("http://camera/live.mp4"))
                .isInstanceOf(RuntimeException.class);
        assertThatThrownBy(
                        () ->
                                CameraStreamProtocol.CRTSP.parse(
                                        "rtsp://user:pass@camera/sf_ingress/0123456789abcdef0123456789abcdef"))
                .isInstanceOf(RuntimeException.class);
        assertThatThrownBy(
                        () -> CameraStreamProtocol.GB28181.parse("gb28181://camera/device/channel"))
                .isInstanceOf(RuntimeException.class);
    }
}
