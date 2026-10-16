package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.ArgumentMatchers.eq;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class CameraStatusExecutionTest {
    @Test
    void workerAndProbeShareEndpointGateAcrossSourcesAndProfilePaths() throws Exception {
        var rules = mock(CameraSourceRules.class);
        when(rules.host(anyString(), eq("fixture"))).thenAnswer(call -> call.getArgument(0));
        var limits = new RtspDescribeProbe.Limits(2, 1, 2, 4096, 4096, 1024, 8);
        var received = new CountDownLatch(1);
        var reply = new CountDownLatch(1);
        try (var listener = new ServerSocket(0, 1, InetAddress.getLoopbackAddress());
                var server = Executors.newSingleThreadExecutor();
                var probe = new RtspDescribeProbe(rules, limits);
                var worker =
                        new CameraStatusWorker(
                                probe,
                                mock(CameraMediaProbe.class),
                                new CameraStatusWorker.Limits(2, 1, Duration.ZERO))) {
            listener.setSoTimeout(3000);
            var response =
                    server.submit(
                            () -> {
                                try (Socket socket = listener.accept()) {
                                    socket.setSoTimeout(3000);
                                    String request = readRequest(socket);
                                    received.countDown();
                                    assertThat(reply.await(3, TimeUnit.SECONDS)).isTrue();
                                    String sdp =
                                            "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=fixture\r\nt=0 0\r\nm=video 0 RTP/AVP 96\r\n";
                                    String headers =
                                            "RTSP/1.0 200 OK\r\nCSeq: 1\r\nContent-Type: application/sdp\r\nContent-Length: "
                                                    + sdp.getBytes(StandardCharsets.UTF_8).length
                                                    + "\r\n\r\n";
                                    socket.getOutputStream()
                                            .write(
                                                    (headers + sdp)
                                                            .getBytes(StandardCharsets.UTF_8));
                                    socket.getOutputStream().flush();
                                    assertThat(socket.getInputStream().read()).isEqualTo(-1);
                                    return request;
                                }
                            });
            try {
                int port = listener.getLocalPort();
                var first = worker.trySubmit(request(1, 1, 1, port, "/main"));
                assertThat(received.await(2, TimeUnit.SECONDS)).isTrue();
                var second = worker.trySubmit(request(2, 2, 2, port, "/sub"));
                var blocked = second.completion().get(2, TimeUnit.SECONDS);
                assertThat(blocked.outcome()).isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
                assertThat(blocked.reasonCode()).isEqualTo("ENDPOINT_BUSY");
                reply.countDown();
                assertThat(first.completion().get(2, TimeUnit.SECONDS).outcome())
                        .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
                assertThat(response.get(2, TimeUnit.SECONDS))
                        .startsWith("DESCRIBE rtsp://")
                        .contains("/main RTSP/1.0", "CSeq: 1", "Accept: application/sdp")
                        .doesNotContain("SETUP", "PLAY", "TEARDOWN");
                worker.close();
                assertThat(worker.awaitStopped(Duration.ofSeconds(2))).isTrue();
            } finally {
                reply.countDown();
            }
        }
    }

    private static CameraStatusWorker.Request request(
            long checkId, long sourceId, long profileId, int port, String path) {
        return new CameraStatusWorker.Request(
                checkId,
                sourceId,
                profileId,
                "fixture-trace",
                new RtspDescribeProbe.Target(
                        URI.create("rtsp://127.0.0.1:" + port + path), "fixture", null, null),
                Duration.ofSeconds(3));
    }

    private static String readRequest(Socket socket) throws Exception {
        var input =
                new BufferedReader(
                        new InputStreamReader(socket.getInputStream(), StandardCharsets.US_ASCII));
        var value = new StringBuilder();
        for (String line; (line = input.readLine()) != null && !line.isEmpty(); )
            value.append(line).append('\n');
        return value.toString();
    }
}
