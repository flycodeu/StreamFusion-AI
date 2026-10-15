package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.adapter.CameraRequestAuthentication;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.net.SocketTimeoutException;
import java.net.URI;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.HashMap;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.Semaphore;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;

/** Internal, explicitly owned RTSP control check; no media session or background camera polling. */
public final class RtspDescribeProbe implements AutoCloseable {
    public record Limits(
            int concurrency,
            int dnsThreads,
            int dnsQueueCapacity,
            int maxHeaderBytes,
            int maxBodyBytes,
            int maxLineBytes,
            int maxDnsAddresses) {
        public Limits {
            if (concurrency < 1
                    || concurrency > 32
                    || dnsThreads < 1
                    || dnsThreads > concurrency
                    || dnsQueueCapacity < 1
                    || dnsQueueCapacity > 128
                    || maxHeaderBytes < 1
                    || maxHeaderBytes > 65_536
                    || maxBodyBytes < 1
                    || maxBodyBytes > 1_048_576
                    || maxLineBytes < 1
                    || maxLineBytes > 16_384
                    || maxLineBytes > maxHeaderBytes
                    || maxDnsAddresses < 1
                    || maxDnsAddresses > 128)
                throw new IllegalArgumentException("Invalid probe limits");
        }
    }

    public record Target(
            URI uri,
            String networkPolicyKey,
            String username,
            String password,
            Runnable authorizationCheck) {
        public Target(URI uri, String networkPolicyKey, String username, String password) {
            this(uri, networkPolicyKey, username, password, () -> {});
        }

        public Target {
            java.util.Objects.requireNonNull(authorizationCheck);
        }

        boolean sameConnection(Target other) {
            return other != null
                    && java.util.Objects.equals(uri, other.uri)
                    && java.util.Objects.equals(networkPolicyKey, other.networkPolicyKey)
                    && java.util.Objects.equals(username, other.username)
                    && java.util.Objects.equals(password, other.password);
        }

        @Override
        public String toString() {
            return "Target[redacted]";
        }
    }

    public enum Outcome {
        ACCESSIBLE,
        FAILED,
        UNSUPPORTED,
        NOT_EXECUTED,
        CANCELLED
    }

    public record Result(Outcome outcome, String reasonCode, int statusCode, long durationMs) {}

    /** One execution's cancellation handle. Socket registration and cancellation share a lock. */
    public static final class Cancellation {
        private volatile boolean cancelled;
        private volatile boolean timedOut;
        private final AtomicBoolean claimed = new AtomicBoolean();
        private volatile CompletableFuture<Void> resourcesStopped =
                CompletableFuture.completedFuture(null);
        private Socket socket;

        public synchronized void cancel() {
            cancelled = true;
            closeSocket();
        }

        public boolean isCancelled() {
            return cancelled;
        }

        /** Result completion is not DNS/resource completion; a target gate waits for the latter. */
        void afterResourcesStopped(Runnable action) {
            resourcesStopped.thenRun(action);
        }

        private synchronized void timeout() {
            timedOut = true;
            closeSocket();
        }

        private synchronized boolean register(Socket next) {
            if (cancelled || timedOut || socket != null) {
                close(next);
                return false;
            }
            socket = next;
            return true;
        }

        private synchronized void release(Socket current) {
            close(current);
            if (socket == current) socket = null;
        }

        private void closeSocket() {
            if (socket != null) close(socket);
        }

        private static void close(Socket socket) {
            try {
                socket.close();
            } catch (IOException ignored) {
                // A closed/failed socket is never reused.
            }
        }
    }

    @FunctionalInterface
    interface Resolver {
        InetAddress[] resolve(String host) throws IOException;
    }

    private final CameraSourceRules rules;
    private final Limits limits;
    private final Resolver resolver;
    private final Semaphore slots;
    private final ThreadPoolExecutor dns;
    private final ScheduledThreadPoolExecutor deadlines;
    private final AtomicBoolean closed = new AtomicBoolean();
    private final Object lifecycle = new Object();
    private final Set<Execution> active = ConcurrentHashMap.newKeySet();
    private final Set<String> endpoints = ConcurrentHashMap.newKeySet();

    public RtspDescribeProbe(CameraSourceRules rules, Limits limits) {
        this(rules, limits, InetAddress::getAllByName);
    }

    /** Narrow DNS hook for deterministic cancellation fixtures, not a protocol registry. */
    RtspDescribeProbe(CameraSourceRules rules, Limits limits, Resolver resolver) {
        this.rules = java.util.Objects.requireNonNull(rules);
        this.limits = java.util.Objects.requireNonNull(limits);
        this.resolver = java.util.Objects.requireNonNull(resolver);
        slots = new Semaphore(limits.concurrency(), true);
        dns =
                new ThreadPoolExecutor(
                        limits.dnsThreads(),
                        limits.dnsThreads(),
                        0,
                        TimeUnit.SECONDS,
                        new ArrayBlockingQueue<>(limits.dnsQueueCapacity()),
                        task -> {
                            var thread = new Thread(task, "camera-status-dns");
                            thread.setDaemon(true);
                            return thread;
                        },
                        new ThreadPoolExecutor.AbortPolicy());
        deadlines =
                new ScheduledThreadPoolExecutor(
                        1,
                        task -> {
                            var thread = new Thread(task, "camera-status-deadline");
                            thread.setDaemon(true);
                            return thread;
                        });
        deadlines.setRemoveOnCancelPolicy(true);
    }

    public Result execute(Target target, long deadlineNanos, Cancellation cancellation) {
        long started = System.nanoTime();
        java.util.Objects.requireNonNull(cancellation);
        if (cancellation.isCancelled()) return result(Outcome.CANCELLED, "CANCELLED", 0, started);
        if (!cancellation.claimed.compareAndSet(false, true))
            return result(Outcome.NOT_EXECUTED, "CANCELLATION_IN_USE", 0, started);
        var execution = new Execution(cancellation, deadlineNanos);
        synchronized (lifecycle) {
            if (closed.get()) return result(Outcome.NOT_EXECUTED, "PROBE_CLOSED", 0, started);
            if (!slots.tryAcquire()) return result(Outcome.NOT_EXECUTED, "PROBE_BUSY", 0, started);
            cancellation.resourcesStopped = new CompletableFuture<>();
            active.add(execution);
        }
        java.util.concurrent.ScheduledFuture<?> timeout = null;
        Resolution resolution = null;
        String endpoint = null;
        int status = 0;
        try {
            execution.check();
            if (deadlineNanos - started > TimeUnit.MINUTES.toNanos(5))
                throw new Failure(Outcome.NOT_EXECUTED, "INVALID_DEADLINE");
            String host = validate(target);
            timeout =
                    deadlines.schedule(
                            cancellation::timeout,
                            Math.max(0, deadlineNanos - System.nanoTime()),
                            TimeUnit.NANOSECONDS);
            authorize(target, execution);
            execution.references.incrementAndGet();
            resolution = new Resolution(host, execution);
            try {
                dns.execute(resolution);
            } catch (RejectedExecutionException ex) {
                resolution.finish();
                throw new Failure(Outcome.NOT_EXECUTED, "DNS_BUSY");
            }
            InetAddress[] addresses = awaitDns(resolution, execution);
            if (addresses == null || addresses.length == 0)
                throw new Failure(Outcome.FAILED, "DNS_UNAVAILABLE");
            if (addresses.length > limits.maxDnsAddresses())
                throw new Failure(Outcome.NOT_EXECUTED, "NETWORK_BLOCKED");
            for (var address : addresses) {
                if (address == null) throw new Failure(Outcome.FAILED, "DNS_UNAVAILABLE");
                allowed(address.getHostAddress(), target.networkPolicyKey());
            }
            execution.check();
            int port = target.uri().getPort() < 0 ? 554 : target.uri().getPort();
            endpoint =
                    addresses[0].getHostAddress().toLowerCase(java.util.Locale.ROOT) + "#" + port;
            if (!endpoints.add(endpoint)) {
                endpoint = null;
                throw new Failure(Outcome.NOT_EXECUTED, "ENDPOINT_BUSY");
            }
            String authorization = null;
            for (int sequence = 1; sequence <= 2; sequence++) {
                execution.check();
                Response response =
                        request(target, addresses[0], port, sequence, authorization, execution);
                status = response.status();
                execution.check();
                if (status == 401 && sequence == 1) {
                    try {
                        authorization =
                                CameraRequestAuthentication.challenge(
                                        response.headers().get("www-authenticate"),
                                        "DESCRIBE",
                                        target.uri().toASCIIString(),
                                        target.username(),
                                        target.password());
                    } catch (CameraAdapterException ex) {
                        throw new Failure(
                                "UNSUPPORTED_AUTHENTICATION".equals(ex.reasonCode())
                                        ? Outcome.UNSUPPORTED
                                        : Outcome.FAILED,
                                "UNSUPPORTED_AUTHENTICATION".equals(ex.reasonCode())
                                        ? "AUTH_UNSUPPORTED"
                                        : "AUTH_REJECTED");
                    }
                    continue;
                }
                if (status == 401 || status == 403)
                    throw new Failure(Outcome.FAILED, "AUTH_REJECTED");
                if (status == 404) throw new Failure(Outcome.FAILED, "RTSP_PATH_NOT_FOUND");
                if (status >= 300 && status < 400)
                    throw new Failure(Outcome.FAILED, "RTSP_REDIRECT_REJECTED");
                if (status != 200) throw new Failure(Outcome.FAILED, "RTSP_REJECTED");
                if (!videoSdp(response)) throw new Failure(Outcome.FAILED, "SDP_INVALID");
                execution.check();
                return result(Outcome.ACCESSIBLE, "RTSP_ACCESSIBLE", status, started);
            }
            throw new Failure(Outcome.FAILED, "AUTH_REJECTED");
        } catch (Failure failure) {
            if (cancellation.isCancelled() || Thread.currentThread().isInterrupted())
                return result(Outcome.CANCELLED, "CANCELLED", status, started);
            return result(failure.outcome, failure.reason, status, started);
        } catch (SocketTimeoutException | TimeoutException ex) {
            return result(
                    cancellation.isCancelled() ? Outcome.CANCELLED : Outcome.FAILED,
                    cancellation.isCancelled() ? "CANCELLED" : "RTSP_TIMEOUT",
                    status,
                    started);
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            cancellation.cancel();
            return result(Outcome.CANCELLED, "CANCELLED", status, started);
        } catch (IOException ex) {
            return result(
                    cancellation.isCancelled() ? Outcome.CANCELLED : Outcome.FAILED,
                    cancellation.isCancelled()
                            ? "CANCELLED"
                            : cancellation.timedOut || System.nanoTime() - deadlineNanos >= 0
                                    ? "RTSP_TIMEOUT"
                                    : "RTSP_CONNECTION_FAILED",
                    status,
                    started);
        } catch (RejectedExecutionException ex) {
            if (cancellation.isCancelled() || Thread.currentThread().isInterrupted())
                return result(Outcome.CANCELLED, "CANCELLED", status, started);
            return result(Outcome.NOT_EXECUTED, "PROBE_CLOSED", status, started);
        } finally {
            if (timeout != null) timeout.cancel(false);
            if (resolution != null && dns.remove(resolution)) resolution.finish();
            if (endpoint != null) endpoints.remove(endpoint);
            execution.release();
        }
    }

    private String validate(Target target) {
        if (target == null
                || target.uri() == null
                || !"rtsp".equalsIgnoreCase(target.uri().getScheme())
                || target.uri().getHost() == null
                || target.uri().getRawUserInfo() != null
                || target.uri().getRawFragment() != null
                || target.uri().getRawPath() == null
                || !target.uri().getRawPath().startsWith("/")
                || target.uri().toASCIIString().length() + 32 > limits.maxLineBytes()
                || target.uri().toString().codePoints().anyMatch(Character::isISOControl)
                || (target.username() == null) != (target.password() == null)
                || invalidSecret(target.username(), 128)
                || invalidSecret(target.password(), 512))
            throw new Failure(Outcome.NOT_EXECUTED, "INVALID_TARGET");
        int port = target.uri().getPort() < 0 ? 554 : target.uri().getPort();
        if (port < 1 || port > 65535) throw new Failure(Outcome.NOT_EXECUTED, "INVALID_TARGET");
        String host = target.uri().getHost().replace("[", "").replace("]", "");
        allowed(host, target.networkPolicyKey());
        return host;
    }

    private static boolean invalidSecret(String value, int maximum) {
        return value != null
                && (value.codePointCount(0, value.length()) > maximum
                        || value.codePoints().anyMatch(Character::isISOControl));
    }

    private void allowed(String host, String policy) {
        try {
            rules.host(host, policy);
        } catch (RuntimeException ex) {
            throw new Failure(Outcome.NOT_EXECUTED, "NETWORK_BLOCKED");
        }
    }

    private InetAddress[] awaitDns(Resolution resolution, Execution execution)
            throws InterruptedException, TimeoutException {
        while (true) {
            execution.check();
            long remaining = execution.deadline - System.nanoTime();
            try {
                return resolution.addresses.get(
                        Math.min(remaining, TimeUnit.MILLISECONDS.toNanos(50)),
                        TimeUnit.NANOSECONDS);
            } catch (ExecutionException ex) {
                if (ex.getCause() instanceof Failure failure) throw failure;
                throw new Failure(Outcome.FAILED, "DNS_UNAVAILABLE");
            } catch (TimeoutException ex) {
                if (System.nanoTime() - execution.deadline >= 0) throw ex;
            }
        }
    }

    private Response request(
            Target target,
            InetAddress address,
            int port,
            int sequence,
            String authorization,
            Execution execution)
            throws IOException {
        var socket = new Socket();
        if (!execution.cancellation.register(socket)) {
            execution.check();
            throw new Failure(Outcome.NOT_EXECUTED, "CANCELLATION_IN_USE");
        }
        try {
            authorize(target, execution);
            socket.connect(new InetSocketAddress(address, port), remainingMillis(execution));
            socket.setSoTimeout(remainingMillis(execution));
            var value =
                    "DESCRIBE "
                            + target.uri().toASCIIString()
                            + " RTSP/1.0\r\nCSeq: "
                            + sequence
                            + "\r\nAccept: application/sdp\r\n"
                            + (authorization == null
                                    ? ""
                                    : "Authorization: " + authorization + "\r\n")
                            + "\r\n";
            byte[] bytes = value.getBytes(StandardCharsets.UTF_8);
            if (bytes.length > limits.maxHeaderBytes())
                throw new Failure(Outcome.NOT_EXECUTED, "REQUEST_LIMIT");
            execution.check();
            socket.getOutputStream().write(bytes);
            socket.getOutputStream().flush();
            execution.check();
            return response(socket.getInputStream(), sequence);
        } finally {
            execution.cancellation.release(socket);
        }
    }

    private static void authorize(Target target, Execution execution) {
        execution.check();
        try {
            target.authorizationCheck().run();
        } catch (CameraAdapterException ex) {
            throw new Failure(Outcome.NOT_EXECUTED, authorizationReason(ex));
        } catch (RuntimeException ex) {
            throw new Failure(Outcome.NOT_EXECUTED, "AUTHORIZATION_UNAVAILABLE");
        }
        execution.check();
    }

    static String authorizationReason(CameraAdapterException exception) {
        return switch (exception.reasonCode() == null ? "" : exception.reasonCode()) {
            case "ACTOR_REVOKED",
                    "CONFIGURATION_CHANGED",
                    "CONFIGURATION_UNAVAILABLE",
                    "TARGET_DELETED",
                    "CHECK_INTERRUPTED",
                    "CHECK_NO_LONGER_ACTIVE",
                    "CHECK_EXPIRED",
                    "SESSION_STORE_UNAVAILABLE" ->
                    exception.reasonCode();
            default -> "AUTHORIZATION_UNAVAILABLE";
        };
    }

    private int remainingMillis(Execution execution) {
        execution.check();
        long millis = TimeUnit.NANOSECONDS.toMillis(execution.deadline - System.nanoTime());
        return (int) Math.max(1, Math.min(Integer.MAX_VALUE, millis));
    }

    private Response response(InputStream input, int sequence) throws IOException {
        int[] count = {0};
        String status = line(input, count);
        if (status.startsWith("RTSP/") && !status.startsWith("RTSP/1.0 "))
            throw new Failure(Outcome.UNSUPPORTED, "RTSP_UNSUPPORTED");
        if (!status.matches("RTSP/1\\.0 [1-5][0-9]{2}(?: [\\x20-\\x7e]*)?"))
            throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
        int code = Integer.parseInt(status.substring(9, 12));
        Map<String, String> headers = new HashMap<>();
        while (true) {
            String value = line(input, count);
            if (value.isEmpty()) break;
            int colon = value.indexOf(':');
            if (colon < 1) throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
            String name = value.substring(0, colon).toLowerCase(java.util.Locale.ROOT);
            if (!name.matches("[a-z0-9-]+"))
                throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
            if (headers.putIfAbsent(name, value.substring(colon + 1).strip()) != null) {
                if (code == 401 && name.equals("www-authenticate"))
                    throw new Failure(Outcome.UNSUPPORTED, "AUTH_UNSUPPORTED");
                throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
            }
        }
        if (!Integer.toString(sequence).equals(headers.get("cseq")))
            throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
        int length = 0;
        if (headers.containsKey("content-length")) {
            String raw = headers.get("content-length");
            try {
                if (!raw.matches("[0-9]+")) throw new NumberFormatException();
                length = Integer.parseInt(raw);
            } catch (NumberFormatException ex) {
                throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
            }
            if (length > limits.maxBodyBytes()) throw new Failure(Outcome.FAILED, "RESPONSE_LIMIT");
        }
        byte[] body = input.readNBytes(length);
        if (body.length != length) throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
        return new Response(code, headers, body);
    }

    private String line(InputStream input, int[] total) throws IOException {
        var bytes = new ByteArrayOutputStream();
        boolean carriage = false;
        while (true) {
            int next = input.read();
            if (next < 0) throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
            if (++total[0] > limits.maxHeaderBytes()
                    || bytes.size() + (carriage ? 1 : 0) >= limits.maxLineBytes())
                throw new Failure(Outcome.FAILED, "RESPONSE_LIMIT");
            if (carriage) {
                if (next != '\n') throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
                return bytes.toString(StandardCharsets.US_ASCII);
            }
            if (next == '\r') carriage = true;
            else {
                if (next != '\t' && (next < 32 || next > 126))
                    throw new Failure(Outcome.FAILED, "RTSP_INVALID_RESPONSE");
                bytes.write(next);
            }
        }
    }

    private boolean videoSdp(Response response) {
        String type = response.headers().get("content-type");
        if (type == null || !type.split(";", 2)[0].strip().equalsIgnoreCase("application/sdp"))
            return false;
        String body;
        try {
            body =
                    StandardCharsets.UTF_8
                            .newDecoder()
                            .onMalformedInput(CodingErrorAction.REPORT)
                            .onUnmappableCharacter(CodingErrorAction.REPORT)
                            .decode(ByteBuffer.wrap(response.body()))
                            .toString();
        } catch (CharacterCodingException ex) {
            return false;
        }
        String[] lines = body.split("\\r?\\n", -1);
        if (lines.length < 5 || !lines[0].equals("v=0")) return false;
        boolean origin = false, session = false, timing = false, media = false, video = false;
        for (int i = 1; i < lines.length; i++) {
            String line = lines[i];
            if (line.isEmpty()) {
                if (i != lines.length - 1) return false;
                continue;
            }
            if (line.getBytes(StandardCharsets.UTF_8).length > limits.maxLineBytes()
                    || line.length() < 2
                    || line.charAt(1) != '='
                    || line.charAt(0) < 'a'
                    || line.charAt(0) > 'z'
                    || line.codePoints().anyMatch(Character::isISOControl)) return false;
            if (line.startsWith("v=")) return false;
            if (line.startsWith("o=")) {
                if (origin || session || timing || media || !validOrigin(line.substring(2)))
                    return false;
                origin = true;
            } else if (line.startsWith("s=")) {
                if (!origin || session || timing || media || line.length() == 2) return false;
                session = true;
            } else if (line.startsWith("t=")) {
                String[] fields = line.substring(2).split(" ", -1);
                if (!session
                        || media
                        || fields.length != 2
                        || !unsigned64(fields[0])
                        || !unsigned64(fields[1])) return false;
                timing = true;
            } else if (line.startsWith("m=")) {
                if (!timing || !validMedia(line.substring(2))) return false;
                media = true;
                if (line.startsWith("m=video ")) video = true;
            }
        }
        return origin && session && timing && video;
    }

    private static boolean validOrigin(String origin) {
        String[] fields = origin.split(" ", -1);
        return fields.length == 6
                && !fields[0].isBlank()
                && unsigned64(fields[1])
                && unsigned64(fields[2])
                && fields[3].equals("IN")
                && (fields[4].equals("IP4") || fields[4].equals("IP6"))
                && !fields[5].isBlank()
                && fields[5].codePoints().noneMatch(Character::isWhitespace)
                && validOriginAddress(fields[4], fields[5]);
    }

    private static boolean validOriginAddress(String family, String value) {
        if (value.indexOf(':') >= 0) return family.equals("IP6") && validIpv6(value);
        if (value.matches("[0-9.]+")) return family.equals("IP4") && validIpv4(value);
        // SDP origin may be a FQDN. It is descriptive only, never resolved or used to connect.
        return value.length() <= 253
                && value.matches(
                        "[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?"
                                + "(?:\\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\\.?");
    }

    private static boolean validIpv4(String value) {
        String[] octets = value.split("\\.", -1);
        if (octets.length != 4) return false;
        for (String octet : octets) if (!boundedNumber(octet, 0, 255)) return false;
        return true;
    }

    private static boolean validIpv6(String value) {
        if (!value.matches("[0-9A-Fa-f:.]+")) return false;
        try {
            // URI brackets validate the literal address without InetAddress/DNS resolution.
            return URI.create("rtsp://[" + value + "]/").getHost() != null;
        } catch (IllegalArgumentException ex) {
            return false;
        }
    }

    private static boolean validMedia(String media) {
        String[] fields = media.split(" ", -1);
        if (fields.length < 4 || !fields[0].matches("[A-Za-z][A-Za-z0-9-]*")) return false;
        String[] port = fields[1].split("/", -1);
        if (port.length > 2
                || !boundedNumber(port[0], 0, 65535)
                || (port.length == 2 && !boundedNumber(port[1], 1, 65535))) return false;
        if (!fields[2].matches("RTP/(?:AVP|AVPF|SAVP|SAVPF)")) return false;
        for (int i = 3; i < fields.length; i++) if (!boundedNumber(fields[i], 0, 127)) return false;
        return true;
    }

    private static boolean unsigned64(String value) {
        if (!value.matches("[0-9]{1,20}")) return false;
        try {
            Long.parseUnsignedLong(value);
            return true;
        } catch (NumberFormatException ex) {
            return false;
        }
    }

    private static boolean boundedNumber(String value, int minimum, int maximum) {
        if (!value.matches("[0-9]{1,5}")) return false;
        int number = Integer.parseInt(value);
        return number >= minimum && number <= maximum;
    }

    private static Result result(Outcome outcome, String reason, int status, long started) {
        return new Result(
                outcome,
                reason,
                status,
                Math.max(0, TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started)));
    }

    @Override
    public void close() {
        synchronized (lifecycle) {
            if (!closed.compareAndSet(false, true)) return;
            active.forEach(execution -> execution.cancellation.cancel());
        }
        for (Runnable queued : dns.shutdownNow()) ((Resolution) queued).finish();
        deadlines.shutdownNow();
    }

    /**
     * False means a resolver/active check is still running; close cannot interrupt system DNS
     * reliably.
     */
    public boolean awaitStopped(Duration timeout) throws InterruptedException {
        java.util.Objects.requireNonNull(timeout);
        if (timeout.isNegative() || timeout.compareTo(Duration.ofMinutes(1)) > 0)
            throw new IllegalArgumentException("Invalid stop timeout");
        long until = System.nanoTime() + timeout.toNanos();
        if (!dns.awaitTermination(timeout.toNanos(), TimeUnit.NANOSECONDS)) return false;
        return deadlines.awaitTermination(
                        Math.max(0, until - System.nanoTime()), TimeUnit.NANOSECONDS)
                && active.isEmpty();
    }

    private final class Execution {
        final Cancellation cancellation;
        final long deadline;
        final AtomicInteger references = new AtomicInteger(1);

        Execution(Cancellation cancellation, long deadline) {
            this.cancellation = cancellation;
            this.deadline = deadline;
        }

        void check() {
            if (cancellation.isCancelled() || Thread.currentThread().isInterrupted())
                throw new Failure(Outcome.CANCELLED, "CANCELLED");
            if (closed.get()) throw new Failure(Outcome.CANCELLED, "CANCELLED");
            if (cancellation.timedOut || System.nanoTime() - deadline >= 0)
                throw new Failure(Outcome.FAILED, "RTSP_TIMEOUT");
        }

        void release() {
            if (references.decrementAndGet() == 0) {
                active.remove(this);
                slots.release();
                cancellation.resourcesStopped.complete(null);
            }
        }
    }

    private final class Resolution implements Runnable {
        final String host;
        final Execution execution;
        final CompletableFuture<InetAddress[]> addresses = new CompletableFuture<>();
        final AtomicBoolean finished = new AtomicBoolean();

        Resolution(String host, Execution execution) {
            this.host = host;
            this.execution = execution;
        }

        @Override
        public void run() {
            try {
                execution.check();
                addresses.complete(resolver.resolve(host));
            } catch (Exception ex) {
                addresses.completeExceptionally(ex);
            } finally {
                finish();
            }
        }

        void finish() {
            if (finished.compareAndSet(false, true)) execution.release();
        }
    }

    private record Response(int status, Map<String, String> headers, byte[] body) {}

    private static final class Failure extends RuntimeException {
        final Outcome outcome;
        final String reason;

        Failure(Outcome outcome, String reason) {
            super(reason);
            this.outcome = outcome;
            this.reason = reason;
        }
    }
}
