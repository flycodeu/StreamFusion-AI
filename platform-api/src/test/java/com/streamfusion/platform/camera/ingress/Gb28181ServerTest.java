package com.streamfusion.platform.camera.ingress;

import static org.assertj.core.api.Assertions.*;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.*;
import org.junit.jupiter.api.Test;

class Gb28181ServerTest {
    static final String DEVICE = "34020000001320000001", CHANNEL = "34020000001320000002";

    @Test
    void simulatedCameraRegistersCataloguesInvitesAcknowledgesAndCloses() throws Exception {
        var config = configuration();
        try (var camera = new DatagramSocket(new InetSocketAddress("127.0.0.1", 0));
                var server = new Gb28181Server(config)) {
            camera.setSoTimeout(3000);
            server.start();
            var target = new InetSocketAddress("127.0.0.1", config.getSipPort());
            String uri = "sip:" + config.getServerId() + "@" + config.getDomain();
            send(camera, target, message("REGISTER", uri, "", "", 1));
            String challenge = receive(camera);
            assertThat(challenge).startsWith("SIP/2.0 401");
            String nonce = challenge.split("nonce=\"")[1].split("\"")[0];
            String response =
                    GbDigest.md5(
                            GbDigest.md5(DEVICE + ":" + config.getDomain() + ":password123")
                                    + ":"
                                    + nonce
                                    + ":00000001:test:auth:"
                                    + GbDigest.md5("REGISTER:" + uri));
            String auth =
                    "Authorization: Digest username=\""
                            + DEVICE
                            + "\", realm=\""
                            + config.getDomain()
                            + "\", nonce=\""
                            + nonce
                            + "\", uri=\""
                            + uri
                            + "\", response=\""
                            + response
                            + "\", qop=auth, nc=00000001, cnonce=\"test\"\r\n";
            send(camera, target, message("REGISTER", uri, auth, "", 2));
            assertThat(receive(camera)).startsWith("SIP/2.0 200");
            assertThat(receive(camera)).startsWith("MESSAGE ");
            send(
                    camera,
                    target,
                    message(
                            "MESSAGE",
                            uri,
                            "",
                            "<Response><CmdType>Catalog</CmdType><DeviceList><Item><DeviceID>"
                                    + CHANNEL
                                    + "</DeviceID></Item></DeviceList></Response>",
                            3));
            assertThat(receive(camera)).startsWith("SIP/2.0 200");
            assertThat(server.registeredCount()).isOne();
            var executor = Executors.newSingleThreadExecutor();
            try {
                var invited =
                        executor.submit(
                                () ->
                                        server.invite(
                                                DEVICE,
                                                CHANNEL,
                                                30000,
                                                1234567890,
                                                PreviewDeadline.after(Duration.ofSeconds(5))));
                String invite = receive(camera);
                assertThat(invite).startsWith("INVITE ").contains("PS/90000", "y=1234567890");
                var parsed = GbSipMessage.parse(invite.getBytes(StandardCharsets.UTF_8));
                String sdp = "v=0\r\nm=video 40000 RTP/AVP 96\r\na=rtpmap:96 PS/90000\r\n";
                String answer =
                        "SIP/2.0 200 OK\r\nVia: "
                                + parsed.header("via")
                                + "\r\nFrom: "
                                + parsed.header("from")
                                + "\r\nTo: "
                                + parsed.header("to")
                                + ";tag=camera\r\nCall-ID: "
                                + parsed.header("call-id")
                                + "\r\nCSeq: 1 INVITE\r\nContent-Length: "
                                + sdp.length()
                                + "\r\n\r\n"
                                + sdp;
                send(camera, target, answer);
                assertThat(receive(camera)).startsWith("ACK ");
                var dialog = invited.get(3, TimeUnit.SECONDS);
                server.bye(dialog);
                assertThat(receive(camera)).startsWith("BYE ");
            } finally {
                executor.shutdownNow();
            }
        }
    }

    @Test
    void xmlEntitiesAndInvalidDigestAreRejected() {
        assertThat(
                        GbDigest.verify(
                                "Digest username=\"wrong\"",
                                DEVICE,
                                "password123",
                                "realm",
                                "nonce",
                                "sip:server"))
                .isFalse();
        String body =
                "<!DOCTYPE x [<!ENTITY leak SYSTEM 'file:///private'>]><Notify><CmdType>&leak;</CmdType></Notify>";
        var message =
                GbSipMessage.parse(
                        message("MESSAGE", "sip:server", "", body, 1)
                                .getBytes(StandardCharsets.UTF_8));
        assertThatThrownBy(message::xml).isInstanceOf(RuntimeException.class);
        assertThatThrownBy(() -> GbSipMessage.parse(new byte[65508]))
                .isInstanceOf(RuntimeException.class);
    }

    static IngressProperties configuration() throws Exception {
        var config = new IngressProperties();
        config.setEnabled(true);
        config.setGbEnabled(true);
        config.setMediaHost("127.0.0.1");
        config.setSipAdvertisedHost("127.0.0.1");
        config.setHookToken("0123456789abcdef0123456789abcdef");
        try (var unused = new DatagramSocket(0)) {
            config.setSipPort(unused.getLocalPort());
        }
        var device = new IngressProperties.Device();
        device.setPassword("password123");
        device.setRemoteIps(Set.of("127.0.0.1"));
        device.setChannels(Set.of(CHANNEL));
        config.setDevices(Map.of(DEVICE, device));
        return config;
    }

    static String message(String method, String uri, String headers, String body, int sequence) {
        return method
                + " "
                + uri
                + " SIP/2.0\r\nVia: SIP/2.0/UDP 127.0.0.1;rport;branch=z9hG4bKtest\r\nFrom: <sip:"
                + DEVICE
                + "@3402000000>;tag=device\r\nTo: <sip:34020000002000000001@3402000000>\r\nCall-ID: register\r\nCSeq: "
                + sequence
                + " "
                + method
                + "\r\n"
                + headers
                + "Content-Length: "
                + body.getBytes(StandardCharsets.UTF_8).length
                + "\r\n\r\n"
                + body;
    }

    static void send(DatagramSocket socket, InetSocketAddress target, String value)
            throws Exception {
        byte[] bytes = value.getBytes(StandardCharsets.UTF_8);
        socket.send(new DatagramPacket(bytes, bytes.length, target));
    }

    static String receive(DatagramSocket socket) throws Exception {
        byte[] bytes = new byte[65507];
        var packet = new DatagramPacket(bytes, bytes.length);
        socket.receive(packet);
        return new String(packet.getData(), 0, packet.getLength(), StandardCharsets.UTF_8);
    }
}
