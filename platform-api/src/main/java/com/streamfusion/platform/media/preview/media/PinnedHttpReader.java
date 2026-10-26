package com.streamfusion.platform.media.preview.media;

import com.streamfusion.platform.media.preview.PreviewFailure;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import javax.net.ssl.*;

/** Streaming HTTP/1.1 with pinned sockets, system TLS trust, no redirects or implicit DNS. */
final class PinnedHttpReader {
    private PinnedHttpReader() {}

    static Socket connect(URI pinned, URI origin) throws IOException {
        String ip = pinned.getHost().replace("[", "").replace("]", "");
        if (ip.contains(":")) {
            if (!ip.matches("[0-9a-fA-F:]+")) throw new PreviewFailure("DESTINATION_REJECTED");
        } else {
            if (!ip.matches("(0|[1-9][0-9]{0,2})(\\.(0|[1-9][0-9]{0,2})){3}"))
                throw new PreviewFailure("DESTINATION_REJECTED");
            for (String octet : ip.split("\\."))
                if (Integer.parseInt(octet) > 255) throw new PreviewFailure("DESTINATION_REJECTED");
        }
        Socket raw = new Socket();
        try {
            raw.connect(new InetSocketAddress(InetAddress.getByName(ip), pinned.getPort()), 3000);
            raw.setSoTimeout(5000);
            if (!"https".equals(origin.getScheme())) return raw;
            var tls =
                    (SSLSocket)
                            ((SSLSocketFactory) SSLSocketFactory.getDefault())
                                    .createSocket(raw, origin.getHost(), origin.getPort(), true);
            var parameters = tls.getSSLParameters();
            parameters.setEndpointIdentificationAlgorithm("HTTPS");
            tls.setSSLParameters(parameters);
            tls.startHandshake();
            return tls;
        } catch (IOException | RuntimeException failure) {
            raw.close();
            throw failure;
        }
    }

    static Response get(Socket socket, URI pinned, URI target, String range) throws IOException {
        if (range != null && !range.matches("bytes=[0-9]{1,12}-[0-9]{0,12}"))
            throw new PreviewFailure("HTTP_MEDIA_RANGE_REJECTED");
        String path =
                target.getRawPath()
                        + (target.getRawQuery() == null ? "" : "?" + target.getRawQuery());
        String auth =
                pinned.getRawUserInfo() == null
                        ? ""
                        : "Authorization: Basic "
                                + Base64.getEncoder()
                                        .encodeToString(
                                                URLDecoder.decode(
                                                                pinned.getRawUserInfo()
                                                                        .replace("+", "%2B"),
                                                                StandardCharsets.UTF_8)
                                                        .getBytes(StandardCharsets.UTF_8))
                                + "\r\n";
        socket.getOutputStream()
                .write(
                        ("GET "
                                        + path
                                        + " HTTP/1.1\r\nHost: "
                                        + target.getRawAuthority()
                                        + "\r\nConnection: close\r\nAccept-Encoding: identity\r\n"
                                        + auth
                                        + (range == null ? "" : "Range: " + range + "\r\n")
                                        + "\r\n")
                                .getBytes(StandardCharsets.US_ASCII));
        var input = new BufferedInputStream(socket.getInputStream());
        String status = line(input);
        if (!status.matches("HTTP/1\\.[01] (200|206)( .*)?"))
            throw new PreviewFailure(
                    status.matches("HTTP/1\\.[01] 3[0-9]{2}( .*)?")
                            ? "HTTP_MEDIA_REDIRECT_REJECTED"
                            : "HTTP_MEDIA_UPSTREAM_REJECTED");
        Map<String, String> headers = new HashMap<>();
        int size = 0;
        while (true) {
            String value = line(input);
            size += value.length();
            if (size > 32768 || headers.size() > 100) throw new IOException();
            if (value.isEmpty()) break;
            int colon = value.indexOf(':');
            if (colon < 1) throw new IOException();
            String key = value.substring(0, colon).toLowerCase(Locale.ROOT);
            // Duplicated framing headers are ambiguous. Ordinary repeated headers are ignored.
            if (headers.containsKey(key)
                    && Set.of("content-length", "transfer-encoding", "content-encoding")
                            .contains(key)) throw new IOException();
            headers.put(key, value.substring(colon + 1).strip());
        }
        if (headers.containsKey("content-encoding")
                && !"identity".equalsIgnoreCase(headers.get("content-encoding")))
            throw new IOException();
        String encoding = headers.get("transfer-encoding");
        if (encoding != null
                && (!"chunked".equalsIgnoreCase(encoding) || headers.containsKey("content-length")))
            throw new IOException();
        InputStream body = encoding == null ? input : new Chunked(input);
        if (headers.containsKey("content-length")) {
            try {
                long length = Long.parseLong(headers.get("content-length"));
                if (length < 0) throw new IOException();
                body = new FixedLength(input, length);
            } catch (NumberFormatException failure) {
                throw new IOException();
            }
        }
        return new Response(
                status.contains(" 206") ? 206 : 200,
                headers.get("content-type"),
                headers.get("content-range"),
                body);
    }

    record Response(int status, String contentType, String contentRange, InputStream body) {}

    private static String line(InputStream input) throws IOException {
        var output = new ByteArrayOutputStream();
        for (int count = 0; count < 8192; count++) {
            int b = input.read();
            if (b == -1) throw new EOFException();
            if (b == '\n')
                return output.toString(StandardCharsets.US_ASCII).replaceFirst("\\r$", "");
            output.write(b);
        }
        throw new IOException();
    }

    private static final class FixedLength extends InputStream {
        private final InputStream input;
        private long remaining;

        FixedLength(InputStream input, long remaining) {
            this.input = input;
            this.remaining = remaining;
        }

        public int read() throws IOException {
            byte[] b = new byte[1];
            return read(b, 0, 1) < 0 ? -1 : b[0] & 255;
        }

        public int read(byte[] b, int off, int len) throws IOException {
            if (len == 0) return 0;
            if (remaining == 0) return -1;
            int count = input.read(b, off, (int) Math.min(len, remaining));
            if (count < 0) throw new EOFException();
            remaining -= count;
            return count;
        }
    }

    private static final class Chunked extends InputStream {
        private final InputStream input;
        private long remaining;
        private boolean first = true, ended;

        Chunked(InputStream input) {
            this.input = input;
        }

        public int read() throws IOException {
            byte[] b = new byte[1];
            return read(b, 0, 1) < 0 ? -1 : b[0] & 255;
        }

        public int read(byte[] b, int off, int len) throws IOException {
            if (len == 0) return 0;
            if (ended) return -1;
            if (remaining == 0) {
                if (!first && !line(input).isEmpty()) throw new IOException();
                first = false;
                try {
                    remaining = Long.parseLong(line(input).split(";", 2)[0], 16);
                } catch (NumberFormatException ex) {
                    throw new IOException();
                }
                if (remaining < 0 || remaining > 64L * 1024 * 1024) throw new IOException();
                if (remaining == 0) {
                    ended = true;
                    return -1;
                }
            }
            int count = input.read(b, off, (int) Math.min(len, remaining));
            if (count < 0) throw new EOFException();
            remaining -= count;
            return count;
        }
    }
}
