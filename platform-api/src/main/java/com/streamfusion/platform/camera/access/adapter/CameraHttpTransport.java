package com.streamfusion.platform.camera.access.adapter;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import jakarta.annotation.PreDestroy;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.SocketTimeoutException;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.SSLException;
import javax.net.ssl.SSLSocket;
import javax.net.ssl.SSLSocketFactory;
import org.springframework.stereotype.Component;

/** Small read-only HTTP/1.1 transport with pinned DNS, system TLS trust and bounded I/O. */
@Component
public class CameraHttpTransport {
    static final int MAX_BODY = 1_048_576;
    private final CameraSourceRules rules;
    private final SSLSocketFactory tlsFactory;
    private final java.util.concurrent.ScheduledThreadPoolExecutor deadlines =
            new java.util.concurrent.ScheduledThreadPoolExecutor(
                    1,
                    runnable -> {
                        Thread thread = new Thread(runnable, "camera-http-deadline");
                        thread.setDaemon(true);
                        return thread;
                    });
    private final ThreadPoolExecutor dns =
            new ThreadPoolExecutor(
                    2,
                    2,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(8),
                    runnable -> {
                        Thread thread = new Thread(runnable, "camera-dns");
                        thread.setDaemon(true);
                        return thread;
                    },
                    new ThreadPoolExecutor.AbortPolicy());

    @org.springframework.beans.factory.annotation.Autowired
    public CameraHttpTransport(CameraSourceRules rules) {
        this(rules, (SSLSocketFactory) SSLSocketFactory.getDefault());
    }

    CameraHttpTransport(CameraSourceRules rules, SSLSocketFactory tlsFactory) {
        this.rules = rules;
        this.tlsFactory = tlsFactory;
        deadlines.setRemoveOnCancelPolicy(true);
    }

    @PreDestroy
    public void close() {
        dns.shutdownNow();
        deadlines.shutdownNow();
    }

    public Session open(CameraAccessContext context) {
        validate(context.endpoint(), context.networkPolicyKey(), false);
        return new Session(context);
    }

    /** Connect-only scan of a literal IPv4 address; sends no protocol or authentication bytes. */
    public boolean tcpOpen(
            String host, int port, String policy, Runnable authorizationCheck, long deadline) {
        authorizationCheck.run();
        if (Thread.currentThread().isInterrupted()) throw new CameraAdapterException("CANCELLED");
        if (!host.matches("[0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+"))
            throw new CameraAdapterException("DESTINATION_REJECTED");
        rules.host(host, policy);
        CameraSourceRules.port(port);
        long remaining = TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime());
        if (remaining <= 0) return false;
        try (Socket socket = new Socket()) {
            socket.connect(
                    new InetSocketAddress(InetAddress.getByName(host), port),
                    (int) Math.min(250, Math.max(1, remaining)));
            authorizationCheck.run();
            return true;
        } catch (IOException ex) {
            authorizationCheck.run();
            return false;
        }
    }

    /** Validates a discovered stream location without connecting to or logging that location. */
    public URI streamUri(CameraAccessContext context, String value) {
        try {
            URI uri = URI.create(value);
            validate(uri, context.networkPolicyKey(), true);
            return uri;
        } catch (IllegalArgumentException ex) {
            throw new CameraAdapterException("INVALID_STREAM_URI");
        }
    }

    void validate(URI uri, String policy, boolean stream) {
        try {
            if (uri == null
                    || uri.getHost() == null
                    || uri.getFragment() != null
                    || uri.toString().length() > 8192
                    || uri.getRawUserInfo() != null
                    || !(stream
                            ? "rtsp".equalsIgnoreCase(uri.getScheme())
                            : "http".equalsIgnoreCase(uri.getScheme())
                                    || "https".equalsIgnoreCase(uri.getScheme())))
                throw new CameraAdapterException("DESTINATION_REJECTED");
            String host = unbracket(uri.getHost());
            rules.host(host, policy);
            if (uri.getPort() != -1) CameraSourceRules.port(uri.getPort());
        } catch (CameraAdapterException ex) {
            throw ex;
        } catch (RuntimeException ex) {
            throw new CameraAdapterException("DESTINATION_REJECTED");
        }
    }

    private InetAddress resolve(URI uri, String policy, int timeoutMs) {
        String host = unbracket(uri.getHost());
        java.util.concurrent.Future<InetAddress[]> future = null;
        try {
            future = dns.submit(() -> InetAddress.getAllByName(host));
            InetAddress[] addresses = future.get(timeoutMs, TimeUnit.MILLISECONDS);
            if (addresses.length == 0 || addresses.length > 16)
                throw new CameraAdapterException("DNS_REJECTED");
            // Every resolved address must satisfy CIDRs, even for an explicitly approved hostname.
            for (InetAddress address : addresses) rules.host(address.getHostAddress(), policy);
            return addresses[0];
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new CameraAdapterException("CANCELLED");
        } catch (java.util.concurrent.TimeoutException ex) {
            throw new CameraAdapterException("CONNECTION_TIMEOUT");
        } catch (RuntimeException | java.util.concurrent.ExecutionException ex) {
            throw new CameraAdapterException("DNS_REJECTED");
        } finally {
            if (future != null) future.cancel(true);
        }
    }

    private static String unbracket(String host) {
        return host.startsWith("[") && host.endsWith("]")
                ? host.substring(1, host.length() - 1)
                : host;
    }

    public final class Session {
        private final CameraAccessContext context;
        private final long deadline;
        private int requests;

        private Session(CameraAccessContext context) {
            this.context = context;
            this.deadline = context.deadlineNanos();
        }

        public byte[] get(URI uri) {
            return request("GET", uri, new byte[0], "application/xml", Map.of(), true);
        }

        public byte[] post(
                URI uri,
                byte[] body,
                String contentType,
                Map<String, String> headers,
                boolean deviceAuth) {
            return request("POST", uri, body, contentType, headers, deviceAuth);
        }

        private byte[] request(
                String method,
                URI uri,
                byte[] body,
                String contentType,
                Map<String, String> headers,
                boolean deviceAuth) {
            var actualHeaders = new LinkedHashMap<>(headers);
            Response response = exchange(method, uri, body, contentType, actualHeaders);
            if (response.status == 401 && deviceAuth) {
                actualHeaders.put(
                        "Authorization",
                        CameraHttpAuthentication.challenge(
                                response.headers.get("www-authenticate"),
                                method,
                                uri,
                                context.username(),
                                context.password()));
                response = exchange(method, uri, body, contentType, actualHeaders);
            }
            if (response.status == 401 || response.status == 403)
                throw new CameraAdapterException("AUTHENTICATION_FAILED");
            if (response.status >= 300 && response.status < 400)
                throw new CameraAdapterException("REDIRECT_REJECTED");
            if (response.status == 404 || response.status == 405 || response.status == 501)
                throw new CameraAdapterException("PROTOCOL_NOT_SUPPORTED");
            if (response.status == 500 && contentType.startsWith("application/soap+xml")) {
                CameraXml.checkSoapFault(CameraXml.parse(response.body));
            }
            if (response.status < 200 || response.status >= 300)
                throw new CameraAdapterException("UPSTREAM_HTTP_ERROR");
            return response.body;
        }

        private Response exchange(
                String method,
                URI uri,
                byte[] body,
                String contentType,
                Map<String, String> headers) {
            validate(uri, context.networkPolicyKey(), false);
            if (!uri.getHost().equalsIgnoreCase(context.endpoint().getHost())
                    || ("https".equalsIgnoreCase(context.endpoint().getScheme())
                            && !"https".equalsIgnoreCase(uri.getScheme())))
                throw new CameraAdapterException("CREDENTIAL_ORIGIN_REJECTED");
            if (++requests > 4096 || body.length > 65536)
                throw new CameraAdapterException("DISCOVERY_LIMIT_EXCEEDED");
            context.checkAuthorization().run();
            InetAddress address =
                    resolve(uri, context.networkPolicyKey(), Math.min(3000, remaining()));
            int port =
                    uri.getPort() == -1
                            ? ("https".equalsIgnoreCase(uri.getScheme()) ? 443 : 80)
                            : uri.getPort();
            try (Socket raw = new Socket()) {
                var timeout =
                        deadlines.schedule(
                                () -> {
                                    try {
                                        raw.close();
                                    } catch (IOException ignored) {
                                    }
                                },
                                remaining(),
                                TimeUnit.MILLISECONDS);
                try {
                    raw.connect(new InetSocketAddress(address, port), Math.min(5000, remaining()));
                    raw.setSoTimeout(Math.min(5000, remaining()));
                    Socket socket = raw;
                    if ("https".equalsIgnoreCase(uri.getScheme())) {
                        SSLSocket tls =
                                (SSLSocket)
                                        tlsFactory.createSocket(
                                                raw, unbracket(uri.getHost()), port, true);
                        var parameters = tls.getSSLParameters();
                        parameters.setEndpointIdentificationAlgorithm("HTTPS");
                        tls.setSSLParameters(parameters);
                        tls.setSoTimeout(Math.min(5000, remaining()));
                        tls.startHandshake();
                        socket = tls;
                    }
                    try (Socket active = socket) {
                        String path = uri.getRawPath().isEmpty() ? "/" : uri.getRawPath();
                        if (uri.getRawQuery() != null) path += "?" + uri.getRawQuery();
                        String host = uri.getHost() + (uri.getPort() == -1 ? "" : ":" + port);
                        StringBuilder head =
                                new StringBuilder(
                                        method
                                                + " "
                                                + path
                                                + " HTTP/1.1\r\nHost: "
                                                + host
                                                + "\r\nConnection: close\r\nAccept-Encoding: identity\r\nContent-Length: "
                                                + body.length
                                                + "\r\nContent-Type: "
                                                + contentType
                                                + "\r\n");
                        for (var entry : headers.entrySet()) {
                            if (!entry.getKey().matches("[A-Za-z0-9-]+")
                                    || entry.getValue()
                                            .codePoints()
                                            .anyMatch(Character::isISOControl))
                                throw new CameraAdapterException("INVALID_PROTOCOL_FIELD");
                            head.append(entry.getKey())
                                    .append(": ")
                                    .append(entry.getValue())
                                    .append("\r\n");
                        }
                        var output = active.getOutputStream();
                        output.write(
                                head.append("\r\n")
                                        .toString()
                                        .getBytes(StandardCharsets.ISO_8859_1));
                        output.write(body);
                        output.flush();
                        return response(active);
                    }
                } finally {
                    timeout.cancel(false);
                }
            } catch (SSLException ex) {
                throw new CameraAdapterException("TLS_VALIDATION_FAILED");
            } catch (SocketTimeoutException ex) {
                throw new CameraAdapterException("CONNECTION_TIMEOUT");
            } catch (IOException ex) {
                remaining();
                throw new CameraAdapterException("CONNECTION_FAILED");
            }
        }

        private int remaining() {
            if (Thread.currentThread().isInterrupted())
                throw new CameraAdapterException("CANCELLED");
            long millis = TimeUnit.NANOSECONDS.toMillis(deadline - System.nanoTime());
            if (millis <= 0) throw new CameraAdapterException("DISCOVERY_TIMEOUT");
            return (int) millis;
        }

        private Response response(Socket socket) throws IOException {
            InputStream input = socket.getInputStream();
            String status = line(socket, input);
            if (!status.matches("HTTP/1\\.[01] [0-9]{3}(?: .*)?"))
                throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
            int code = Integer.parseInt(status.substring(9, 12));
            Map<String, String> headers = new LinkedHashMap<>();
            int total = status.length();
            while (true) {
                String line = line(socket, input);
                total += line.length();
                if (total > 32768) throw new CameraAdapterException("RESPONSE_TOO_LARGE");
                if (line.isEmpty()) break;
                int colon = line.indexOf(':');
                if (colon < 1) throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
                String key = line.substring(0, colon).toLowerCase(Locale.ROOT);
                String value = line.substring(colon + 1).strip();
                if (headers.containsKey(key)
                        && java.util.Set.of(
                                        "content-length", "transfer-encoding", "content-encoding")
                                .contains(key))
                    throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
                if (!headers.containsKey(key)
                        || value.toLowerCase(Locale.ROOT).startsWith("digest "))
                    headers.put(key, value);
            }
            String encoding = headers.get("content-encoding");
            if (encoding != null && !encoding.equalsIgnoreCase("identity"))
                throw new CameraAdapterException("UNSUPPORTED_RESPONSE_ENCODING");
            var bytes = new ByteArrayOutputStream();
            String transfer = headers.get("transfer-encoding");
            if (transfer != null) {
                if (!transfer.equalsIgnoreCase("chunked") || headers.containsKey("content-length"))
                    throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
                while (true) {
                    String sizeLine = line(socket, input).split(";", 2)[0];
                    int size = length(sizeLine, 16);
                    if (size == 0) break;
                    copy(socket, input, bytes, size);
                    if (!line(socket, input).isEmpty())
                        throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
                }
            } else if (headers.containsKey("content-length")) {
                copy(socket, input, bytes, length(headers.get("content-length"), 10));
            } else {
                byte[] buffer = new byte[8192];
                while (true) {
                    socket.setSoTimeout(Math.min(5000, remaining()));
                    int count = input.read(buffer);
                    if (count < 0) break;
                    if (bytes.size() + count > MAX_BODY)
                        throw new CameraAdapterException("RESPONSE_TOO_LARGE");
                    bytes.write(buffer, 0, count);
                }
            }
            return new Response(code, headers, bytes.toByteArray());
        }

        private int length(String value, int radix) {
            try {
                int result = Integer.parseInt(value, radix);
                if (result < 0 || result > MAX_BODY)
                    throw new CameraAdapterException("RESPONSE_TOO_LARGE");
                return result;
            } catch (NumberFormatException ex) {
                throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
            }
        }

        private void copy(Socket socket, InputStream input, ByteArrayOutputStream bytes, int count)
                throws IOException {
            if (count + bytes.size() > MAX_BODY)
                throw new CameraAdapterException("RESPONSE_TOO_LARGE");
            byte[] buffer = new byte[Math.min(8192, Math.max(1, count))];
            while (count > 0) {
                socket.setSoTimeout(Math.min(5000, remaining()));
                int size = input.read(buffer, 0, Math.min(count, buffer.length));
                if (size < 0) throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
                bytes.write(buffer, 0, size);
                count -= size;
            }
        }

        private String line(Socket socket, InputStream input) throws IOException {
            var bytes = new ByteArrayOutputStream();
            int previous = -1;
            while (bytes.size() <= 8192) {
                socket.setSoTimeout(Math.min(5000, remaining()));
                int value = input.read();
                if (value < 0) throw new CameraAdapterException("INVALID_HTTP_RESPONSE");
                if (value == '\n' && previous == '\r') {
                    byte[] line = bytes.toByteArray();
                    return new String(line, 0, line.length - 1, StandardCharsets.ISO_8859_1);
                }
                bytes.write(value);
                previous = value;
            }
            throw new CameraAdapterException("RESPONSE_TOO_LARGE");
        }
    }

    private record Response(int status, Map<String, String> headers, byte[] body) {}
}
