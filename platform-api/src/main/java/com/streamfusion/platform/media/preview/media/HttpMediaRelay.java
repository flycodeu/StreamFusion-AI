package com.streamfusion.platform.media.preview.media;

import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.source.PinnedMediaUri;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import jakarta.annotation.PreDestroy;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.*;
import java.util.regex.Pattern;
import org.springframework.stereotype.Component;

/**
 * Loopback transport only: no decode, arbitrary URLs, redirects, cookies or cross-origin HLS. Each
 * session belongs to an existing shared ZLM upstream and closes with that exact owner.
 */
@Component
public final class HttpMediaRelay implements AutoCloseable {
    private static final Pattern ATTRIBUTE = Pattern.compile("URI=\"([^\"]*)\"");
    private final Map<String, Session> sessions = new ConcurrentHashMap<>();
    private final Map<String, Session> owners = new ConcurrentHashMap<>();
    private final ThreadPoolExecutor workers =
            new ThreadPoolExecutor(
                    0,
                    32,
                    30,
                    TimeUnit.SECONDS,
                    new SynchronousQueue<>(),
                    r -> daemon(r, "http-media-relay"),
                    new ThreadPoolExecutor.AbortPolicy());
    private final ScheduledThreadPoolExecutor timeouts =
            new ScheduledThreadPoolExecutor(1, r -> daemon(r, "http-media-deadline"));
    private HttpServer server;
    private boolean closed;

    public HttpMediaRelay() {
        timeouts.setRemoveOnCancelPolicy(true);
    }

    private static Thread daemon(Runnable runnable, String name) {
        var thread = new Thread(runnable, name);
        thread.setDaemon(true);
        return thread;
    }

    public synchronized URI open(String owner, URI pinned) {
        if (closed) throw new PreviewFailure("HTTP_MEDIA_UNAVAILABLE");
        close(owner);
        if (sessions.size() >= 64) throw new PreviewFailure("HTTP_MEDIA_CAPACITY");
        try {
            if (server == null) {
                server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 16);
                server.createContext("/", this::serve);
                server.setExecutor(workers);
                server.start();
            }
            String host = PinnedMediaUri.originHost(pinned);
            String authority =
                    (host.contains(":") ? "[" + host + "]" : host) + ":" + pinned.getPort();
            URI origin =
                    URI.create(
                            pinned.getScheme()
                                    + "://"
                                    + authority
                                    + pinned.getRawPath()
                                    + (pinned.getRawQuery() == null
                                            ? ""
                                            : "?" + pinned.getRawQuery()));
            var session =
                    new Session(UUID.randomUUID().toString().replace("-", ""), pinned, origin);
            sessions.put(session.token, session);
            owners.put(owner, session);
            return URI.create(
                    base(session)
                            + resource(
                                    session,
                                    origin,
                                    origin.getPath().toLowerCase(Locale.ROOT).endsWith(".m3u8")));
        } catch (IOException | IllegalArgumentException ex) {
            throw new PreviewFailure("HTTP_MEDIA_UNAVAILABLE");
        }
    }

    public synchronized void close(String owner) {
        var session = owners.remove(owner);
        if (session == null) return;
        sessions.remove(session.token);
        session.closed = true;
        for (var exchange : session.exchanges) exchange.close();
        for (var socket : session.sockets)
            try {
                socket.close();
            } catch (IOException ignored) {
            }
    }

    public String failure(String owner) {
        var session = owners.get(owner);
        return session == null ? null : session.failure;
    }

    int sessionCount() {
        return sessions.size();
    }

    private String base(Session session) {
        return "http://127.0.0.1:" + server.getAddress().getPort() + "/" + session.token + "/";
    }

    private synchronized String resource(Session session, URI uri, boolean playlist) {
        requireOrigin(session.origin, uri);
        String suffix = playlist ? ".m3u8" : uri.equals(session.origin) ? ".flv" : ".ts";
        for (var entry : session.resources.entrySet())
            if (entry.getValue().equals(uri) && entry.getKey().endsWith(suffix))
                return entry.getKey();
        String id = UUID.randomUUID().toString().replace("-", "") + suffix;
        if (session.resources.size() >= 1024)
            session.resources.remove(session.resources.keySet().iterator().next());
        session.resources.put(id, uri);
        return id;
    }

    static void requireOrigin(URI origin, URI uri) {
        if (!Set.of("http", "https").contains(uri.getScheme())
                || !Objects.equals(origin.getScheme(), uri.getScheme())
                || !Objects.equals(origin.getHost(), uri.getHost())
                || port(origin) != port(uri)
                || uri.getRawUserInfo() != null
                || uri.getFragment() != null
                || uri.toASCIIString().length() > 8192
                || uri.getRawPath() == null
                || !uri.getRawPath().startsWith("/"))
            throw new PreviewFailure("HTTP_MEDIA_ORIGIN_REJECTED");
    }

    private static int port(URI uri) {
        return uri.getPort() < 0 ? "https".equals(uri.getScheme()) ? 443 : 80 : uri.getPort();
    }

    String rewrite(Session session, URI playlist, String text) {
        text = text.startsWith("\uFEFF") ? text.substring(1) : text;
        if (!text.startsWith("#EXTM3U") || text.length() > 1024 * 1024)
            throw new PreviewFailure("HLS_PLAYLIST_INVALID");
        var result = new StringBuilder();
        boolean variant = false;
        for (String line : text.split("\\r?\\n")) {
            String stripped = line.strip();
            if (stripped.startsWith("#EXT-X-KEY:") && !stripped.equals("#EXT-X-KEY:METHOD=NONE"))
                throw new PreviewFailure("HLS_ENCRYPTION_UNSUPPORTED");
            if (stripped.startsWith("#EXT-X-MAP:") || stripped.startsWith("#EXT-X-PART"))
                throw new PreviewFailure("HLS_FMP4_UNSUPPORTED");
            if (stripped.startsWith("#")) {
                if (stripped.startsWith("#EXT-X-")) {
                    String tag = stripped.split(":", 2)[0];
                    if (!Set.of(
                                    "#EXT-X-VERSION",
                                    "#EXT-X-TARGETDURATION",
                                    "#EXT-X-MEDIA-SEQUENCE",
                                    "#EXT-X-DISCONTINUITY-SEQUENCE",
                                    "#EXT-X-ENDLIST",
                                    "#EXT-X-PLAYLIST-TYPE",
                                    "#EXT-X-INDEPENDENT-SEGMENTS",
                                    "#EXT-X-PROGRAM-DATE-TIME",
                                    "#EXT-X-BYTERANGE",
                                    "#EXT-X-DISCONTINUITY",
                                    "#EXT-X-STREAM-INF",
                                    "#EXT-X-MEDIA",
                                    "#EXT-X-I-FRAME-STREAM-INF",
                                    "#EXT-X-START",
                                    "#EXT-X-ALLOW-CACHE",
                                    "#EXT-X-KEY")
                            .contains(tag)) throw new PreviewFailure("HLS_PLAYLIST_INVALID");
                }
                if (line.contains("URI=") && !ATTRIBUTE.matcher(line).find())
                    throw new PreviewFailure("HLS_PLAYLIST_INVALID");
                var matcher = ATTRIBUTE.matcher(line);
                var output = new StringBuilder();
                while (matcher.find()) {
                    boolean childPlaylist =
                            stripped.startsWith("#EXT-X-MEDIA:")
                                    || stripped.startsWith("#EXT-X-I-FRAME-STREAM-INF:");
                    if (!childPlaylist) throw new PreviewFailure("HLS_PLAYLIST_INVALID");
                    matcher.appendReplacement(
                            output,
                            java.util.regex.Matcher.quoteReplacement(
                                    "URI=\""
                                            + base(session)
                                            + resource(
                                                    session,
                                                    playlist.resolve(matcher.group(1)),
                                                    true)
                                            + "\""));
                }
                matcher.appendTail(output);
                result.append(output);
                if (stripped.startsWith("#EXT-X-STREAM-INF:")) variant = true;
            } else if (!stripped.isEmpty()) {
                result.append(base(session))
                        .append(
                                resource(
                                        session,
                                        playlist.resolve(stripped),
                                        variant
                                                || stripped.split("\\?", 2)[0]
                                                        .toLowerCase(Locale.ROOT)
                                                        .endsWith(".m3u8")));
                variant = false;
            }
            result.append('\n');
        }
        return result.toString();
    }

    private void serve(HttpExchange exchange) throws IOException {
        Session session = null;
        Socket socket = null;
        ScheduledFuture<?> expiry = null;
        boolean headers = false, reserved = false;
        try {
            if (!"GET".equals(exchange.getRequestMethod()))
                throw new PreviewFailure("HTTP_MEDIA_REQUEST_REJECTED");
            String[] path = exchange.getRequestURI().getPath().split("/", -1);
            if (path.length != 3 || exchange.getRequestURI().getRawQuery() != null)
                throw new PreviewFailure("HTTP_MEDIA_REQUEST_REJECTED");
            session = sessions.get(path[1]);
            URI target;
            synchronized (this) {
                target = session == null ? null : session.resources.get(path[2]);
            }
            if (target == null || session.closed || !session.permits.tryAcquire())
                throw new PreviewFailure("HTTP_MEDIA_REQUEST_REJECTED");
            reserved = true;
            session.exchanges.add(exchange);
            socket = PinnedHttpReader.connect(session.pinned, target);
            session.sockets.add(socket);
            Socket active = socket;
            expiry =
                    timeouts.schedule(
                            () -> {
                                exchange.close();
                                try {
                                    active.close();
                                } catch (IOException ignored) {
                                }
                            },
                            path[2].endsWith(".flv") ? 86400 : 20,
                            TimeUnit.SECONDS);
            if (session.closed) throw new IOException();
            var response =
                    PinnedHttpReader.get(
                            socket,
                            session.pinned,
                            target,
                            exchange.getRequestHeaders().getFirst("Range"));
            InputStream body = response.body();
            if (path[2].endsWith(".m3u8")) {
                byte[] bytes = body.readNBytes(1024 * 1024 + 1);
                if (bytes.length > 1024 * 1024) throw new PreviewFailure("HLS_PLAYLIST_INVALID");
                byte[] playlist =
                        rewrite(session, target, new String(bytes, StandardCharsets.UTF_8))
                                .getBytes(StandardCharsets.UTF_8);
                exchange.getResponseHeaders().set("Content-Type", "application/vnd.apple.mpegurl");
                exchange.sendResponseHeaders(200, playlist.length);
                headers = true;
                exchange.getResponseBody().write(playlist);
            } else {
                byte[] prefix = body.readNBytes(8);
                if (new String(prefix, StandardCharsets.US_ASCII).startsWith("#EXTM3U"))
                    throw new PreviewFailure("HLS_PLAYLIST_INVALID");
                if (response.status() == 206) {
                    if (response.contentRange() == null
                            || !response.contentRange().matches("bytes [0-9]+-[0-9]+/[0-9]+"))
                        throw new IOException();
                    exchange.getResponseHeaders().set("Content-Range", response.contentRange());
                }
                exchange.getResponseHeaders()
                        .set(
                                "Content-Type",
                                path[2].endsWith(".flv") ? "video/x-flv" : "video/mp2t");
                exchange.sendResponseHeaders(response.status(), 0);
                headers = true;
                exchange.getResponseBody().write(prefix);
                byte[] buffer = new byte[32768];
                long bytes = prefix.length;
                int count;
                while (!session.closed && (count = body.read(buffer)) != -1) {
                    bytes += count;
                    if (!path[2].endsWith(".flv") && bytes > 64L * 1024 * 1024)
                        throw new PreviewFailure("HTTP_MEDIA_RESOURCE_TOO_LARGE");
                    exchange.getResponseBody().write(buffer, 0, count);
                }
            }
        } catch (RuntimeException | IOException failure) {
            if (session != null && !session.closed)
                session.failure =
                        failure instanceof PreviewFailure safe
                                ? safe.reason()
                                : "HTTP_MEDIA_UPSTREAM_UNAVAILABLE";
            if (!headers) exchange.sendResponseHeaders(502, -1);
        } finally {
            if (expiry != null) expiry.cancel(false);
            if (socket != null) {
                if (session != null) session.sockets.remove(socket);
                socket.close();
            }
            if (reserved) session.permits.release();
            if (session != null) session.exchanges.remove(exchange);
            exchange.close();
        }
    }

    static final class Session {
        final String token;
        final URI pinned, origin;
        final LinkedHashMap<String, URI> resources = new LinkedHashMap<>();
        final Set<Socket> sockets = ConcurrentHashMap.newKeySet();
        final Set<HttpExchange> exchanges = ConcurrentHashMap.newKeySet();
        final Semaphore permits = new Semaphore(8);
        volatile boolean closed;
        volatile String failure;

        Session(String token, URI pinned, URI origin) {
            this.token = token;
            this.pinned = pinned;
            this.origin = origin;
        }
    }

    @PreDestroy
    public synchronized void close() {
        closed = true;
        for (String owner : List.copyOf(owners.keySet())) close(owner);
        if (server != null) server.stop(0);
        workers.shutdownNow();
        timeouts.shutdownNow();
    }
}
