package com.streamfusion.platform.camera.ingress;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import jakarta.annotation.PostConstruct;
import jakarta.annotation.PreDestroy;
import java.io.IOException;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.stereotype.Component;

/**
 * Minimal GB28181 live-view signalling. UDP SIP, Digest registration, Catalog/Keepalive,
 * INVITE/ACK/CANCEL/BYE. RTP/PS decode stays entirely in ZLM. No unsolicited asset writes.
 */
@Component
public final class Gb28181Server implements AutoCloseable {
    private final IngressProperties config;
    private final Map<String, Registration> registrations = new ConcurrentHashMap<>();
    private final Map<String, Challenge> challenges = new ConcurrentHashMap<>();
    private final Map<String, Dialog> dialogs = new ConcurrentHashMap<>();
    private volatile DatagramSocket socket;
    private volatile boolean closed;
    private Thread listener;

    public Gb28181Server(IngressProperties config) {
        this.config = config;
    }

    @PostConstruct
    public void start() {
        config.validate();
        if (!config.isEnabled() || !config.isGbEnabled()) return;
        try {
            socket =
                    new DatagramSocket(
                            new InetSocketAddress(config.getSipBindHost(), config.getSipPort()));
            socket.setSoTimeout(1000);
            listener = new Thread(this::listen, "gb28181-sip");
            listener.setDaemon(true);
            listener.start();
        } catch (IOException failure) {
            throw new IllegalStateException("GB_SIP_BIND_FAILED");
        }
    }

    public boolean ready() {
        return socket != null && !closed;
    }

    public int registeredCount() {
        return (int) registrations.values().stream().filter(this::fresh).count();
    }

    public Dialog invite(
            String deviceId, String channelId, int port, long ssrc, PreviewDeadline deadline) {
        if (!ready()) throw new PreviewFailure("GB_NOT_CONFIGURED");
        var registration = registrations.get(deviceId);
        if (registration == null || !fresh(registration))
            throw new PreviewFailure("GB_DEVICE_NOT_REGISTERED");
        if (!registration.channels.contains(channelId))
            throw new PreviewFailure("GB_CHANNEL_NOT_FOUND");
        if (dialogs.size() >= config.getMaxSessions() + 64)
            throw new PreviewFailure("GB_SESSION_CAPACITY");
        var dialog =
                new Dialog(
                        UUID.randomUUID().toString(),
                        UUID.randomUUID().toString().replace("-", ""),
                        "z9hG4bK" + UUID.randomUUID().toString().replace("-", ""),
                        deviceId,
                        channelId,
                        registration.remote);
        dialogs.put(dialog.callId, dialog);
        String transport = config.getRtpTcpMode() == 1 ? "TCP/RTP/AVP" : "RTP/AVP";
        String sdp =
                "v=0\r\no="
                        + config.getServerId()
                        + " 0 0 IN IP4 "
                        + config.getMediaHost()
                        + "\r\ns=Play\r\nc=IN IP4 "
                        + config.getMediaHost()
                        + "\r\nt=0 0\r\nm=video "
                        + port
                        + " "
                        + transport
                        + " 96\r\na=recvonly\r\na=rtpmap:96 PS/90000\r\n"
                        + (config.getRtpTcpMode() == 1
                                ? "a=setup:passive\r\na=connection:new\r\n"
                                : "")
                        + "y="
                        + String.format(Locale.ROOT, "%010d", ssrc)
                        + "\r\n";
        byte[] request = request(dialog, "INVITE", sdp);
        long started = System.nanoTime();
        long next = started;
        int attempts = 0;
        try {
            while (System.nanoTime() - started < TimeUnit.SECONDS.toNanos(8)) {
                deadline.check();
                if (System.nanoTime() - next >= 0 && attempts < 4) {
                    send(dialog.remote, request);
                    next = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(500L << attempts++);
                }
                try {
                    var response =
                            dialog.response.get(
                                    Math.min(
                                            TimeUnit.MILLISECONDS.toNanos(100),
                                            deadline.remaining().toNanos()),
                                    TimeUnit.NANOSECONDS);
                    if (!response.start().startsWith("SIP/2.0 200 "))
                        throw new PreviewFailure("GB_INVITE_REJECTED");
                    String answer = new String(response.body(), StandardCharsets.US_ASCII);
                    String transportPattern =
                            config.getRtpTcpMode() == 1 ? "TCP/RTP/AVP" : "RTP/AVP";
                    var mediaLine =
                            java.util.regex.Pattern.compile(
                                            "(?m)^m=video ([0-9]{1,5}) "
                                                    + transportPattern
                                                    + " 96\\r?$")
                                    .matcher(answer);
                    if (!mediaLine.find()
                            || Integer.parseInt(mediaLine.group(1)) < 1
                            || Integer.parseInt(mediaLine.group(1)) > 65535
                            || !answer.contains("a=rtpmap:96 PS/90000")
                            || config.getRtpTcpMode() == 1 && !answer.contains("a=setup:active"))
                        throw new PreviewFailure("GB_SDP_UNSUPPORTED");
                    return dialog;
                } catch (TimeoutException waiting) {
                    /* finite INVITE budget */
                } catch (ExecutionException failure) {
                    throw new PreviewFailure("GB_INVITE_REJECTED");
                }
            }
            throw new PreviewFailure("GB_INVITE_TIMEOUT");
        } catch (InterruptedException failure) {
            Thread.currentThread().interrupt();
            abandon(dialog);
            throw new PreviewFailure("CANCELLED");
        } catch (RuntimeException failure) {
            abandon(dialog);
            throw failure;
        }
    }

    private void abandon(Dialog dialog) {
        dialog.abandoned = true;
        dialog.retireAt = System.nanoTime() + TimeUnit.SECONDS.toNanos(32);
        try {
            send(dialog.remote, request(dialog, dialog.remoteTo == null ? "CANCEL" : "BYE", ""));
        } catch (RuntimeException unavailable) {
            /* Keep the original failure and retired dialog for a late final response. */
        }
    }

    public void bye(Dialog dialog) {
        if (dialog == null) return;
        dialog.abandoned = true;
        dialog.retireAt = System.nanoTime() + TimeUnit.SECONDS.toNanos(32);
        send(dialog.remote, request(dialog, "BYE", ""));
    }

    private boolean fresh(Registration registration) {
        long now = System.nanoTime();
        return now - registration.expires < 0
                && now - registration.lastSeen < TimeUnit.SECONDS.toNanos(90);
    }

    private void listen() {
        long nextWarning = 0;
        while (!closed) {
            long now = System.nanoTime();
            challenges.values().removeIf(c -> now - c.expires >= 0);
            registrations.values().removeIf(r -> now - r.expires >= 0);
            dialogs.values().removeIf(d -> d.retireAt != 0 && now - d.retireAt >= 0);
            try {
                byte[] bytes = new byte[65508];
                var packet = new DatagramPacket(bytes, bytes.length);
                socket.receive(packet);
                handle(
                        Arrays.copyOf(bytes, packet.getLength()),
                        (InetSocketAddress) packet.getSocketAddress());
            } catch (SocketTimeoutException idle) {
                // Expiry runs for busy and idle sockets alike.
            } catch (IOException | RuntimeException failure) {
                if (!closed && System.nanoTime() - nextWarning >= 0) {
                    try (var ignored = MDC.putCloseable("traceId", "gb-sip")) {
                        LoggerFactory.getLogger(getClass())
                                .warn("GB SIP datagram rejected reasonCode=GB_SIGNAL_INVALID");
                    }
                    nextWarning = System.nanoTime() + TimeUnit.SECONDS.toNanos(30);
                }
            }
        }
    }

    void handle(byte[] bytes, InetSocketAddress remote) {
        if (config.getDevices().values().stream()
                .noneMatch(d -> d.getRemoteIps().contains(remote.getAddress().getHostAddress())))
            return;
        GbSipMessage message = GbSipMessage.parse(bytes);
        if ("RESPONSE".equals(message.method())) {
            var dialog = dialogs.get(message.header("call-id"));
            if (dialog == null
                    || !dialog.remote.equals(remote)
                    || !"1 INVITE".equals(message.header("cseq"))) return;
            if (message.start().matches("SIP/2.0 [2-6][0-9]{2} .*")) {
                if (!message.header("to").contains(";tag=")) return;
                if (!message.header("from").contains(";tag=" + dialog.tag)
                        || !message.header("via").contains("branch=" + dialog.branch)) return;
                dialog.transactionAck = !message.start().startsWith("SIP/2.0 2");
                dialog.remoteTo = message.header("to");
                send(remote, request(dialog, "ACK", ""));
                dialog.response.complete(message);
                if (dialog.abandoned && message.start().startsWith("SIP/2.0 200 "))
                    send(remote, request(dialog, "BYE", ""));
            }
            return;
        }
        String id = message.fromId();
        var device = config.getDevices().get(id);
        if (device == null || !device.getRemoteIps().contains(remote.getAddress().getHostAddress()))
            return;
        if ("REGISTER".equals(message.method())) {
            register(message, remote, id, device);
            return;
        }
        var registration = registrations.get(id);
        if (registration == null || !fresh(registration) || !registration.remote.equals(remote)) {
            reply(message, remote, 403, "Forbidden", "");
            return;
        }
        if ("MESSAGE".equals(message.method())) {
            var xml = message.xml();
            String command = GbSipMessage.text(xml, "CmdType");
            if ("Keepalive".equals(command)) registration.lastSeen = System.nanoTime();
            else if ("Catalog".equals(command)) {
                var items = xml.getElementsByTagName("Item");
                if (items.getLength() > 256)
                    throw new IllegalArgumentException("GB_CATALOG_TOO_LARGE");
                for (int i = 0; i < items.getLength(); i++) {
                    var ids =
                            ((org.w3c.dom.Element) items.item(i)).getElementsByTagName("DeviceID");
                    if (ids.getLength() != 1) continue;
                    String channel = ids.item(0).getTextContent().strip();
                    if (channel.matches("[0-9]{20}")
                            && (device.getChannels().isEmpty()
                                    || device.getChannels().contains(channel))
                            && registration.channels.size() < 256)
                        registration.channels.add(channel);
                }
            } else {
                reply(message, remote, 400, "Bad Request", "");
                return;
            }
            reply(message, remote, 200, "OK", "");
        } else if ("BYE".equals(message.method())) {
            var dialog = dialogs.get(message.header("call-id"));
            if (dialog != null && dialog.remote.equals(remote)) dialog.abandoned = true;
            reply(message, remote, 200, "OK", "");
        } else reply(message, remote, 405, "Method Not Allowed", "");
    }

    private void register(
            GbSipMessage message,
            InetSocketAddress remote,
            String id,
            IngressProperties.Device device) {
        String key = id + "/" + remote;
        var challenge = challenges.get(key);
        String uri = message.start().split(" ", 3)[1];
        if (challenge == null
                || System.nanoTime() - challenge.expires >= 0
                || !GbDigest.verify(
                        message.header("authorization"),
                        id,
                        device.getPassword(),
                        config.getDomain(),
                        challenge.nonce,
                        uri)) {
            if (challenges.size() >= 256 && !challenges.containsKey(key)) return;
            String nonce = UUID.randomUUID().toString().replace("-", "");
            challenges.put(
                    key, new Challenge(nonce, System.nanoTime() + TimeUnit.SECONDS.toNanos(60)));
            reply(
                    message,
                    remote,
                    401,
                    "Unauthorized",
                    "WWW-Authenticate: Digest realm=\""
                            + config.getDomain()
                            + "\", nonce=\""
                            + nonce
                            + "\", algorithm=MD5, qop=\"auth\"\r\n");
            return;
        }
        challenges.remove(key, challenge);
        int expires;
        try {
            expires = Integer.parseInt(message.headers().getOrDefault("expires", "3600"));
        } catch (NumberFormatException failure) {
            reply(message, remote, 400, "Bad Request", "");
            return;
        }
        if (expires == 0) registrations.remove(id);
        else {
            expires = Math.max(60, Math.min(3600, expires));
            var old = registrations.get(id);
            var registration =
                    new Registration(remote, System.nanoTime() + TimeUnit.SECONDS.toNanos(expires));
            if (old != null && old.remote.equals(remote))
                registration.channels.addAll(old.channels);
            registrations.put(id, registration);
        }
        reply(message, remote, 200, "OK", "Expires: " + expires + "\r\n");
        if (expires > 0) {
            var catalog =
                    new Dialog(
                            UUID.randomUUID().toString(),
                            UUID.randomUUID().toString().replace("-", ""),
                            "z9hG4bK" + UUID.randomUUID(),
                            id,
                            id,
                            remote);
            send(
                    remote,
                    request(
                            catalog,
                            "MESSAGE",
                            "<?xml version=\"1.0\" encoding=\"UTF-8\"?><Query><CmdType>Catalog</CmdType><SN>1</SN><DeviceID>"
                                    + id
                                    + "</DeviceID></Query>"));
        }
    }

    private void reply(
            GbSipMessage message,
            InetSocketAddress remote,
            int status,
            String reason,
            String extra) {
        String to = message.header("to");
        if (!to.contains(";tag=")) to += ";tag=sf-gb";
        String via =
                message.header("via")
                        .replaceAll(";rport(?:=[0-9]+)?(?=;|$)", ";rport=" + remote.getPort());
        send(
                remote,
                ("SIP/2.0 "
                                + status
                                + " "
                                + reason
                                + "\r\nVia: "
                                + via
                                + "\r\nFrom: "
                                + message.header("from")
                                + "\r\nTo: "
                                + to
                                + "\r\nCall-ID: "
                                + message.header("call-id")
                                + "\r\nCSeq: "
                                + message.header("cseq")
                                + "\r\n"
                                + extra
                                + "Content-Length: 0\r\n\r\n")
                        .getBytes(StandardCharsets.UTF_8));
    }

    private byte[] request(Dialog dialog, String method, String body) {
        String target =
                "sip:"
                        + dialog.channel
                        + "@"
                        + dialog.remote.getAddress().getHostAddress()
                        + ":"
                        + dialog.remote.getPort();
        String local = config.getSipAdvertisedHost() + ":" + config.getSipPort();
        String to = dialog.remoteTo == null ? "<" + target + ">" : dialog.remoteTo;
        String branch =
                "INVITE".equals(method)
                                || "CANCEL".equals(method)
                                || "ACK".equals(method) && dialog.transactionAck
                        ? dialog.branch
                        : "z9hG4bK" + UUID.randomUUID().toString().replace("-", "");
        return (method
                        + " "
                        + target
                        + " SIP/2.0\r\nVia: SIP/2.0/UDP "
                        + local
                        + ";rport;branch="
                        + branch
                        + "\r\nFrom: <sip:"
                        + config.getServerId()
                        + "@"
                        + config.getDomain()
                        + ">;tag="
                        + dialog.tag
                        + "\r\nTo: "
                        + to
                        + "\r\nCall-ID: "
                        + dialog.callId
                        + "\r\nCSeq: "
                        + ("BYE".equals(method) ? 2 : 1)
                        + " "
                        + method
                        + "\r\nContact: <sip:"
                        + config.getServerId()
                        + "@"
                        + local
                        + ">\r\nMax-Forwards: 70\r\n"
                        + ("INVITE".equals(method)
                                ? "Subject: "
                                        + dialog.channel
                                        + ":0,"
                                        + config.getServerId()
                                        + ":0\r\n"
                                : "")
                        + (body.isEmpty()
                                ? ""
                                : "Content-Type: "
                                        + ("INVITE".equals(method)
                                                ? "application/sdp"
                                                : "Application/MANSCDP+xml")
                                        + "\r\n")
                        + "Content-Length: "
                        + body.getBytes(StandardCharsets.UTF_8).length
                        + "\r\n\r\n"
                        + body)
                .getBytes(StandardCharsets.UTF_8);
    }

    private void send(InetSocketAddress remote, byte[] bytes) {
        DatagramSocket active = socket;
        if (closed || active == null) return;
        try {
            active.send(new DatagramPacket(bytes, bytes.length, remote));
        } catch (IOException failure) {
            throw new PreviewFailure("GB_SIGNAL_UNAVAILABLE");
        }
    }

    public static final class Dialog {
        final String callId, tag, branch, device, channel;
        final InetSocketAddress remote;
        final CompletableFuture<GbSipMessage> response = new CompletableFuture<>();
        volatile String remoteTo;
        volatile boolean abandoned;
        volatile boolean transactionAck;
        volatile long retireAt;

        Dialog(
                String callId,
                String tag,
                String branch,
                String device,
                String channel,
                InetSocketAddress remote) {
            this.callId = callId;
            this.tag = tag;
            this.branch = branch;
            this.device = device;
            this.channel = channel;
            this.remote = remote;
        }

        @Override
        public String toString() {
            return "GbDialog[redacted]";
        }
    }

    private record Challenge(String nonce, long expires) {
        @Override
        public String toString() {
            return "Challenge[redacted]";
        }
    }

    private static final class Registration {
        final InetSocketAddress remote;
        final long expires;
        volatile long lastSeen = System.nanoTime();
        final Set<String> channels = ConcurrentHashMap.newKeySet();

        Registration(InetSocketAddress remote, long expires) {
            this.remote = remote;
            this.expires = expires;
        }
    }

    @PreDestroy
    public void close() {
        if (closed) return;
        for (var dialog : dialogs.values())
            if (!dialog.abandoned)
                try {
                    bye(dialog);
                } catch (RuntimeException ignored) {
                }
        closed = true;
        if (socket != null) socket.close();
        if (listener != null)
            try {
                listener.join(1500);
            } catch (InterruptedException failure) {
                Thread.currentThread().interrupt();
            }
        for (var dialog : dialogs.values())
            dialog.response.completeExceptionally(new PreviewFailure("GB_SIGNAL_UNAVAILABLE"));
        registrations.clear();
        challenges.clear();
        dialogs.clear();
    }
}
