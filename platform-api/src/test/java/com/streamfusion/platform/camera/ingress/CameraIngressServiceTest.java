package com.streamfusion.platform.camera.ingress;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.execution.PreviewRuntimeProvider;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.*;
import java.nio.file.Path;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class CameraIngressServiceTest {
    @TempDir Path directory;
    private final ObjectMapper json = new ObjectMapper();

    @Test
    void publishTokensAreWriteOnlyAndExactNamespaceIsRequired() throws Exception {
        var config = Gb28181ServerTest.configuration();
        String id = "0123456789abcdef0123456789abcdef",
                token = "a-secret-token-at-least-thirty-two-characters";
        config.setPublishTokens(Map.of(id, token));
        try (var journal = new GbRtpJournal(directory, json);
                var service =
                        new CameraIngressService(
                                config,
                                mock(Gb28181Server.class),
                                mock(IngressZlmClient.class),
                                mock(PreviewRuntimeProvider.class),
                                journal,
                                json)) {
            var request =
                    json.createObjectNode()
                            .put("schema", "rtsp")
                            .put("vhost", "__defaultVhost__")
                            .put("app", "sf_ingress")
                            .put("stream", id)
                            .put("ip", "10.0.1.5");
            assertThat(service.authorizedPublish(request)).isFalse();
            request.put("params", "sf_token=" + token);
            assertThat(service.authorizedPublish(request)).isTrue();
            request.put("params", "sf_token=" + token + "&sf_token=" + token);
            assertThat(service.authorizedPublish(request)).isFalse();
            request.put("params", "sf_token=&sf_token=" + token);
            assertThat(service.authorizedPublish(request)).isFalse();
            request.put("params", "sf_token=" + token).put("schema", "rtmp");
            assertThat(service.authorizedPublish(request)).isFalse();
            request.put("schema", "rtsp").put("app", "unowned");
            assertThat(service.authorizedPublish(request)).isFalse();
            assertThat(service.status().toString())
                    .doesNotContain(token, config.getHookToken(), "password123");
        }
    }

    @Test
    void gbReceiverIsJournalledSharedAndExactlyClosed() throws Exception {
        var config = Gb28181ServerTest.configuration();
        var runtime =
                new ActiveZlm(
                        URI.create("http://127.0.0.1:8088"),
                        "secret",
                        directory.resolve("MediaServer"),
                        directory.resolve("zlm.ini"),
                        "test");
        var runtimes = mock(PreviewRuntimeProvider.class);
        when(runtimes.activeZlm()).thenReturn(Optional.of(runtime));
        var sip = mock(Gb28181Server.class);
        when(sip.ready()).thenReturn(true);
        var dialog =
                new Gb28181Server.Dialog(
                        "call",
                        "tag",
                        "branch",
                        Gb28181ServerTest.DEVICE,
                        Gb28181ServerTest.CHANNEL,
                        new InetSocketAddress("127.0.0.1", 12345));
        when(sip.invite(anyString(), anyString(), anyInt(), anyLong(), any()))
                .thenThrow(new PreviewFailure("GB_INVITE_TIMEOUT"))
                .thenReturn(dialog);
        var zlm = mock(IngressZlmClient.class);
        doThrow(new PreviewFailure("GB_RECOVERY_REQUIRED"))
                .doNothing()
                .when(zlm)
                .close(any(), any(), any());
        var settings =
                json.createObjectNode()
                        .put("rtsp.port", config.getRtspPort())
                        .put("hook.enable", "1")
                        .put(
                                "hook.on_publish",
                                "http://127.0.0.1:"
                                        + config.getHookPort()
                                        + "/publish/"
                                        + config.getHookToken());
        when(zlm.configuration(any(), any())).thenReturn(settings);
        var receiver = new AtomicReference<Map<String, String>>();
        when(zlm.call(any(), anyString(), anyMap(), any()))
                .thenAnswer(
                        invocation -> {
                            String method = invocation.getArgument(1);
                            Map<String, String> values = invocation.getArgument(2);
                            if (method.equals("openRtpServer")) {
                                receiver.set(values);
                                return json.createObjectNode()
                                        .put("port", Integer.parseInt(values.get("port")));
                            }
                            if (method.equals("listRtpServer"))
                                return json.createObjectNode()
                                        .set(
                                                "data",
                                                json.createArrayNode()
                                                        .add(
                                                                json.createObjectNode()
                                                                        .put(
                                                                                "app",
                                                                                receiver.get()
                                                                                        .get("app"))
                                                                        .put(
                                                                                "stream_id",
                                                                                receiver.get()
                                                                                        .get(
                                                                                                "stream_id"))
                                                                        .put(
                                                                                "port",
                                                                                Integer.parseInt(
                                                                                        receiver.get()
                                                                                                .get(
                                                                                                        "port")))));
                            throw new AssertionError(method);
                        });
        try (var journal = new GbRtpJournal(directory, json);
                var service = new CameraIngressService(config, sip, zlm, runtimes, journal, json)) {
            URI source =
                    URI.create(
                            "gb28181://127.0.0.1:554/"
                                    + Gb28181ServerTest.DEVICE
                                    + "/"
                                    + Gb28181ServerTest.CHANNEL);
            assertThatThrownBy(
                            () ->
                                    service.resolve(
                                            "GB28181",
                                            source,
                                            () -> {},
                                            PreviewDeadline.after(Duration.ofSeconds(10))))
                    .hasMessage("GB_INVITE_TIMEOUT");
            assertThat(journal.entries()).hasSize(1);
            // Retry must retire the exact failed receiver before allocating its replacement.
            URI first =
                    service.resolve(
                            "GB28181",
                            source,
                            () -> {},
                            PreviewDeadline.after(Duration.ofSeconds(10)));
            assertThat(first.getScheme()).isEqualTo("rtsp");
            assertThat(journal.entries()).hasSize(1);
            assertThat(
                            service.resolve(
                                    "GB28181",
                                    source,
                                    () -> {},
                                    PreviewDeadline.after(Duration.ofSeconds(10))))
                    .isEqualTo(first);
            verify(sip, times(2)).invite(anyString(), anyString(), anyInt(), anyLong(), any());
            service.close();
            verify(sip).bye(dialog);
            verify(zlm, times(3)).close(eq(runtime), any(), any());
            assertThat(journal.entries()).isEmpty();
        }
    }

    @Test
    void disabledIngressNeverOpensSocketsAndFailsExplicitly() {
        var config = new IngressProperties();
        try (var journal = new GbRtpJournal(directory, json);
                var service =
                        new CameraIngressService(
                                config,
                                mock(Gb28181Server.class),
                                mock(IngressZlmClient.class),
                                mock(PreviewRuntimeProvider.class),
                                journal,
                                json)) {
            service.start();
            assertThatThrownBy(
                            () ->
                                    service.resolve(
                                            "CRTSP",
                                            URI.create(
                                                    "rtsp://127.0.0.1:554/sf_ingress/0123456789abcdef0123456789abcdef"),
                                            () -> {},
                                            PreviewDeadline.after(Duration.ofSeconds(1))))
                    .isInstanceOf(PreviewFailure.class)
                    .hasMessage("INGRESS_NOT_CONFIGURED");
        }
    }
}
