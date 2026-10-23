package com.streamfusion.platform.storage.s3;

import jakarta.annotation.PreDestroy;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import javax.net.ssl.*;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/**
 * Pinned HTTP/1.1 transport with system TLS trust, fixed deadlines and streaming response bodies.
 */
@Component
@RequiredArgsConstructor
public class StorageHttpTransport {
    private final StorageNetwork network;
    private final Semaphore slots = new Semaphore(16);
    private final ScheduledThreadPoolExecutor deadlines =
            new ScheduledThreadPoolExecutor(
                    1,
                    runnable -> {
                        var t = new Thread(runnable, "storage-http-deadline");
                        t.setDaemon(true);
                        return t;
                    });

    {
        deadlines.setRemoveOnCancelPolicy(true);
    }

    public Response exchange(
            URI endpoint,
            String policy,
            String method,
            String target,
            Map<String, String> headers,
            byte[] body,
            boolean streaming) {
        if (!slots.tryAcquire()) throw new StorageRemoteException("STORAGE_BUSY", 429);
        Socket raw = new Socket();
        var released = new java.util.concurrent.atomic.AtomicBoolean();
        ScheduledFuture<?> timeout = null;
        try {
            if (!target.startsWith("/") || target.codePoints().anyMatch(Character::isISOControl))
                throw new IOException();
            int port =
                    endpoint.getPort() == -1
                            ? (endpoint.getScheme().equals("https") ? 443 : 80)
                            : endpoint.getPort();
            var address = network.resolve(endpoint, policy);
            timeout =
                    deadlines.schedule(
                            () -> {
                                try {
                                    raw.close();
                                } catch (IOException ignored) {
                                } finally {
                                    if (released.compareAndSet(false, true)) slots.release();
                                }
                            },
                            streaming ? 600 : 10,
                            TimeUnit.SECONDS);
            raw.connect(new InetSocketAddress(address, port), 3000);
            raw.setSoTimeout(streaming ? 30000 : 10000);
            Socket socket = raw;
            if (endpoint.getScheme().equals("https")) {
                var tls =
                        (SSLSocket)
                                ((SSLSocketFactory) SSLSocketFactory.getDefault())
                                        .createSocket(raw, endpoint.getHost(), port, true);
                var parameters = tls.getSSLParameters();
                parameters.setEndpointIdentificationAlgorithm("HTTPS");
                tls.setSSLParameters(parameters);
                tls.startHandshake();
                socket = tls;
            }
            var head =
                    new StringBuilder(
                            method
                                    + " "
                                    + target
                                    + " HTTP/1.1\r\nConnection: close\r\nContent-Length: "
                                    + body.length
                                    + "\r\n");
            for (var entry : headers.entrySet()) {
                if (!entry.getKey().matches("[a-zA-Z0-9-]+")
                        || entry.getValue().codePoints().anyMatch(Character::isISOControl))
                    throw new IOException();
                head.append(entry.getKey()).append(": ").append(entry.getValue()).append("\r\n");
            }
            var output = socket.getOutputStream();
            output.write(head.append("\r\n").toString().getBytes(StandardCharsets.ISO_8859_1));
            output.write(body);
            output.flush();
            var input = new BufferedInputStream(socket.getInputStream());
            String statusLine = line(input);
            String[] status = statusLine.split(" ", 3);
            if (status.length < 2 || !status[0].startsWith("HTTP/1.")) throw new IOException();
            int code = Integer.parseInt(status[1]);
            var responseHeaders = new TreeMap<String, String>(String.CASE_INSENSITIVE_ORDER);
            int total = statusLine.length();
            for (String row; !(row = line(input)).isEmpty(); ) {
                total += row.length();
                if (total > 32768 || responseHeaders.size() > 100) throw new IOException();
                int colon = row.indexOf(':');
                if (colon < 1) throw new IOException();
                String name = row.substring(0, colon).toLowerCase(Locale.ROOT),
                        value = row.substring(colon + 1).strip();
                if (responseHeaders.putIfAbsent(name, value) != null) throw new IOException();
            }
            InputStream responseBody = input;
            if (method.equals("HEAD") || code == 204 || code == 304)
                responseBody = InputStream.nullInputStream();
            else if (responseHeaders.containsKey("transfer-encoding")) {
                if (!"chunked".equalsIgnoreCase(responseHeaders.get("transfer-encoding"))
                        || responseHeaders.containsKey("content-length")) throw new IOException();
                responseBody = new Chunked(input);
            } else if (responseHeaders.containsKey("content-length")) {
                long length = Long.parseLong(responseHeaders.get("content-length"));
                if (length < 0) throw new IOException();
                responseBody = new Limited(input, length);
            }
            return new Response(
                    code, responseHeaders, responseBody, socket, raw, timeout, slots, released);
        } catch (Exception ex) {
            try {
                raw.close();
            } catch (IOException ignored) {
            }
            if (timeout != null) timeout.cancel(false);
            if (released.compareAndSet(false, true)) slots.release();
            if (ex instanceof StorageRemoteException remote) throw remote;
            throw new StorageRemoteException(
                    ex instanceof SSLException ? "STORAGE_TLS_ERROR" : "STORAGE_NETWORK_ERROR", 0);
        }
    }

    private static String line(InputStream input) throws IOException {
        var bytes = new ByteArrayOutputStream();
        int last = -1;
        while (bytes.size() <= 8192) {
            int next = input.read();
            if (next < 0) throw new EOFException();
            if (last == '\r' && next == '\n') {
                byte[] value = bytes.toByteArray();
                return new String(value, 0, value.length - 1, StandardCharsets.ISO_8859_1);
            }
            bytes.write(next);
            last = next;
        }
        throw new IOException("Header too large");
    }

    public record Response(
            int status,
            Map<String, String> headers,
            InputStream body,
            Socket socket,
            Socket raw,
            ScheduledFuture<?> timeout,
            Semaphore slots,
            java.util.concurrent.atomic.AtomicBoolean released)
            implements AutoCloseable {
        public byte[] bytes(int limit) {
            try {
                byte[] value = body.readNBytes(limit + 1);
                if (value.length > limit) throw new IOException();
                return value;
            } catch (IOException ex) {
                throw new StorageRemoteException("STORAGE_RESPONSE_INVALID", status);
            }
        }

        @Override
        public void close() {
            timeout.cancel(false);
            try {
                socket.close();
                raw.close();
            } catch (IOException ignored) {
            } finally {
                if (released.compareAndSet(false, true)) slots.release();
            }
        }
    }

    private static class Limited extends FilterInputStream {
        private long remaining;

        Limited(InputStream input, long remaining) {
            super(input);
            this.remaining = remaining;
        }

        @Override
        public int read() throws IOException {
            if (remaining == 0) return -1;
            int b = in.read();
            if (b < 0) throw new EOFException();
            remaining--;
            return b;
        }

        @Override
        public int read(byte[] bytes, int offset, int length) throws IOException {
            if (remaining == 0) return -1;
            if (length == 0) return 0;
            int n = in.read(bytes, offset, (int) Math.min(length, remaining));
            if (n < 0) throw new EOFException();
            remaining -= n;
            return n;
        }
    }

    private static final class Chunked extends FilterInputStream {
        private long remaining;
        private boolean ended;
        private boolean first = true;

        Chunked(InputStream input) {
            super(input);
        }

        private void next() throws IOException {
            if (ended || remaining > 0) return;
            if (!first && !line(in).isEmpty()) throw new IOException();
            first = false;
            String value = line(in).split(";", 2)[0];
            try {
                remaining = Long.parseLong(value, 16);
            } catch (NumberFormatException ex) {
                throw new IOException();
            }
            if (remaining < 0) throw new IOException();
            if (remaining == 0) {
                int count = 0;
                while (!line(in).isEmpty()) if (++count > 30) throw new IOException();
                ended = true;
            }
        }

        @Override
        public int read() throws IOException {
            byte[] one = new byte[1];
            return read(one, 0, 1) == -1 ? -1 : one[0] & 255;
        }

        @Override
        public int read(byte[] bytes, int offset, int length) throws IOException {
            if (length == 0) return 0;
            next();
            if (ended) return -1;
            int n = in.read(bytes, offset, (int) Math.min(length, remaining));
            if (n < 0) throw new EOFException();
            remaining -= n;
            return n;
        }
    }

    @PreDestroy
    public void close() {
        deadlines.shutdownNow();
    }
}
