package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Base64;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Loopback Socket fixtures only; no device, media session, ZLM, or external DNS is contacted. */
class RtspDescribeProbeTest {
    private static final String SDP =
            "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=fixture\r\nt=0 0\r\n"
                    + "m=video 0 RTP/AVP 96\r\na=rtpmap:96 H264/90000\r\n";
    private static final RtspDescribeProbe.Limits LIMITS =
            new RtspDescribeProbe.Limits(2, 1, 2, 8192, 16384, 2048, 8);
    private CameraSourceRules rules;
    private final List<RtspDescribeProbe> probes = new ArrayList<>();
    private final List<Fixture> fixtures = new ArrayList<>();
    private ExecutorService calls;

    @BeforeEach
    void start() {
        rules = mock(CameraSourceRules.class);
        // Production network policy remains unchanged; only fixture mocks allow loopback.
        when(rules.host(anyString(), anyString())).thenAnswer(call -> call.getArgument(0));
        calls = Executors.newFixedThreadPool(2);
    }

    @AfterEach
    void stop() throws Exception {
        probes.forEach(RtspDescribeProbe::close);
        for (Fixture fixture : fixtures) fixture.close();
        calls.shutdownNow();
        assertThat(calls.awaitTermination(2, TimeUnit.SECONDS)).isTrue();
        for (RtspDescribeProbe probe : probes)
            assertThat(probe.awaitStopped(Duration.ofSeconds(2))).isTrue();
    }

    @Test
    void describeUsesExactAbsoluteUriAndNeverStartsMediaSession() throws Exception {
        var clientClosed = new CountDownLatch(1);
        var fixture =
                fixture(
                        (socket, request, index) -> {
                            reply(
                                    socket,
                                    200,
                                    request.cseq(),
                                    Map.of(),
                                    SDP.getBytes(StandardCharsets.UTF_8));
                            if (socket.getInputStream().read() == -1) clientClosed.countDown();
                        });
        var target = target(fixture, "/live%2Fopaque?token=%26", null, null);
        assertThat(execute(probe(LIMITS), target).outcome())
                .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
        assertThat(clientClosed.await(1, TimeUnit.SECONDS)).isTrue();
        assertThat(fixture.requests).hasSize(1);
        assertThat(fixture.requests.getFirst().line())
                .isEqualTo("DESCRIBE " + target.uri() + " RTSP/1.0");
        assertThat(fixture.requests.getFirst().headers())
                .containsEntry("accept", "application/sdp")
                .doesNotContainKey("authorization");
        assertThat(target.toString()).isEqualTo("Target[redacted]");
    }

    @Test
    void basicChallengeIsRetriedOnceOnNewConnection() throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        index == 1 ? 401 : 200,
                                        request.cseq(),
                                        index == 1
                                                ? Map.of(
                                                        "WWW-Authenticate",
                                                        "Basic realm=\"fixture\"")
                                                : Map.of(),
                                        (index == 1 ? "challenge-body" : SDP)
                                                .getBytes(StandardCharsets.UTF_8)));
        var result = execute(probe(LIMITS), target(fixture, "/live", "operator", "private"));
        assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
        assertThat(fixture.requests).hasSize(2);
        assertThat(fixture.requests.get(0).headers()).doesNotContainKey("authorization");
        assertThat(fixture.requests.get(1).headers())
                .containsEntry("cseq", "2")
                .containsEntry(
                        "authorization",
                        "Basic "
                                + Base64.getEncoder()
                                        .encodeToString(
                                                "operator:private"
                                                        .getBytes(StandardCharsets.UTF_8)));
        assertThat(fixture.requests).allMatch(request -> request.line().startsWith("DESCRIBE "));
    }

    @Test
    void digestUsesDescribeAndOriginalAbsoluteRequestTarget() throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        index == 1 ? 401 : 200,
                                        request.cseq(),
                                        index == 1
                                                ? Map.of(
                                                        "WWW-Authenticate",
                                                        "Digest realm=\"fixture\", nonce=\"abc\", algorithm=MD5")
                                                : Map.of(),
                                        (index == 1 ? "" : SDP).getBytes(StandardCharsets.UTF_8)));
        var target = target(fixture, "/live%2Fopaque?q=%26", "operator", "private");
        assertThat(execute(probe(LIMITS), target).outcome())
                .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
        String expected =
                md5(md5("operator:fixture:private") + ":abc:" + md5("DESCRIBE:" + target.uri()));
        assertThat(fixture.requests.get(1).headers().get("authorization"))
                .contains("uri=\"" + target.uri() + "\"", "response=\"" + expected + "\"");
    }

    @Test
    void originFqdnIsValidForBothFamiliesButNeverResolvedOrUsedToConnect() throws Exception {
        var resolverHosts = new CopyOnWriteArrayList<String>();
        var probe =
                probe(
                        LIMITS,
                        host -> {
                            resolverHosts.add(host);
                            return new InetAddress[] {InetAddress.getByName("127.0.0.1")};
                        });
        for (String family : List.of("IP4", "IP6")) {
            String body =
                    SDP.replace("IN IP4 127.0.0.1", "IN " + family + " origin.example.invalid");
            var fixture =
                    fixture(
                            (socket, request, index) ->
                                    reply(
                                            socket,
                                            200,
                                            request.cseq(),
                                            Map.of(),
                                            body.getBytes(StandardCharsets.UTF_8)));
            assertThat(execute(probe, target(fixture, "/live", null, null)).outcome())
                    .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
        }
        assertThat(resolverHosts).containsExactly("127.0.0.1", "127.0.0.1");
        verify(rules, never()).host(eq("origin.example.invalid"), anyString());
    }

    @Test
    void repeatedChallengeStopsAfterTwoDescribeRequests() throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        401,
                                        request.cseq(),
                                        Map.of("WWW-Authenticate", "Basic realm=\"fixture\""),
                                        new byte[0]));
        var result = execute(probe(LIMITS), target(fixture, "/live", "operator", "wrong"));
        assertThat(result.reasonCode()).isEqualTo("AUTH_REJECTED");
        assertThat(fixture.requests).hasSize(2);
    }

    @Test
    void authenticationRetrySharesOriginalDeadlineInsteadOfReceivingNewBudget() throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) -> {
                            Thread.sleep(index == 1 ? 250 : 350);
                            reply(
                                    socket,
                                    index == 1 ? 401 : 200,
                                    request.cseq(),
                                    index == 1
                                            ? Map.of("WWW-Authenticate", "Basic realm=\"fixture\"")
                                            : Map.of(),
                                    (index == 1 ? "" : SDP).getBytes(StandardCharsets.UTF_8));
                        });
        var result =
                probe(LIMITS)
                        .execute(
                                target(fixture, "/live", "operator", "private"),
                                deadline(500),
                                new RtspDescribeProbe.Cancellation());
        assertThat(result.reasonCode()).isEqualTo("RTSP_TIMEOUT");
        assertThat(result.durationMs()).isLessThan(1500);
        assertThat(fixture.requests)
                .hasSize(2)
                .allMatch(request -> request.line().startsWith("DESCRIBE "));
    }

    @Test
    void forbiddenMissingPathAndRedirectHaveDistinctReasonsWithoutFollowing() throws Exception {
        int[] statuses = {403, 404, 302};
        String[] reasons = {"AUTH_REJECTED", "RTSP_PATH_NOT_FOUND", "RTSP_REDIRECT_REJECTED"};
        for (int i = 0; i < statuses.length; i++) {
            int status = statuses[i];
            var fixture =
                    fixture(
                            (socket, request, index) ->
                                    reply(
                                            socket,
                                            status,
                                            request.cseq(),
                                            Map.of("Location", "rtsp://external.invalid/next"),
                                            new byte[0]));
            assertThat(execute(probe(LIMITS), target(fixture, "/live", null, null)).reasonCode())
                    .isEqualTo(reasons[i]);
            assertThat(fixture.requests).hasSize(1);
        }
    }

    @Test
    void malformedAndAudioOnlyDescriptionsNeverBecomeAccessible() throws Exception {
        String[] invalid = {
            SDP.replace("o=- 1 1 IN IP4 127.0.0.1", "o=x"),
            SDP.replace("IN IP4 127.0.0.1", "IN IP6 1:2"),
            SDP.replace("m=video 0 RTP/AVP 96", "m=video 999999999 RTP/AVP 96"),
            SDP.replace("m=video 0 RTP/AVP 96", "m=video 0 RTP/AVP 128"),
            SDP.replace("m=video", "m=audio"),
            "a=ignored\r\n" + SDP,
            SDP.replace("t=0 0", "t=not-time"),
            SDP.replace("v=0", "v=1"),
            SDP.replace("a=rtpmap", "garbage m=video 0 RTP/AVP 96\r\na=rtpmap")
        };
        for (String body : invalid) {
            var fixture =
                    fixture(
                            (socket, request, index) ->
                                    reply(
                                            socket,
                                            200,
                                            request.cseq(),
                                            Map.of(),
                                            body.getBytes(StandardCharsets.UTF_8)));
            assertThat(execute(probe(LIMITS), target(fixture, "/live", null, null)).reasonCode())
                    .isEqualTo("SDP_INVALID");
        }
        byte[] body = SDP.getBytes(StandardCharsets.UTF_8);
        body[body.length - 3] = (byte) 0xc3;
        var fixture =
                fixture(
                        (socket, request, index) ->
                                reply(socket, 200, request.cseq(), Map.of(), body));
        assertThat(execute(probe(LIMITS), target(fixture, "/live", null, null)).reasonCode())
                .isEqualTo("SDP_INVALID");
    }

    @Test
    void responseLimitsAndCseqAreEnforced() throws Exception {
        var wrongSequence =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        200,
                                        "9",
                                        Map.of(),
                                        SDP.getBytes(StandardCharsets.UTF_8)));
        assertThat(execute(probe(LIMITS), target(wrongSequence, "/live", null, null)).reasonCode())
                .isEqualTo("RTSP_INVALID_RESPONSE");
        var largeBody =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        200,
                                        request.cseq(),
                                        Map.of(),
                                        SDP.getBytes(StandardCharsets.UTF_8)));
        var smallBody = new RtspDescribeProbe.Limits(1, 1, 1, 8192, 32, 2048, 8);
        assertThat(execute(probe(smallBody), target(largeBody, "/live", null, null)).reasonCode())
                .isEqualTo("RESPONSE_LIMIT");
        var largeHeader =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        200,
                                        request.cseq(),
                                        Map.of("X-Large", "x".repeat(300)),
                                        new byte[0]));
        var smallLine = new RtspDescribeProbe.Limits(1, 1, 1, 8192, 16384, 128, 8);
        assertThat(execute(probe(smallLine), target(largeHeader, "/live", null, null)).reasonCode())
                .isEqualTo("RESPONSE_LIMIT");
    }

    @Test
    void twoAuthenticationHeadersAreExplicitlyUnsupported() throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) ->
                                socket.getOutputStream()
                                        .write(
                                                ("RTSP/1.0 401 Unauthorized\r\nCSeq: 1\r\nWWW-Authenticate: Basic realm=\"a\"\r\n"
                                                                + "WWW-Authenticate: Digest realm=\"a\", nonce=\"b\"\r\nContent-Length: 0\r\n\r\n")
                                                        .getBytes(StandardCharsets.US_ASCII)));
        var result = execute(probe(LIMITS), target(fixture, "/live", "operator", "private"));
        assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.UNSUPPORTED);
        assertThat(result.reasonCode()).isEqualTo("AUTH_UNSUPPORTED");
        assertThat(fixture.requests).hasSize(1);
    }

    @Test
    void authorizationRevokedBeforeDnsOrSocketNeverConnectsAndIsNotDeviceFailure()
            throws Exception {
        for (int revokedAt : new int[] {1, 2}) {
            var fixture = fixture((socket, request, index) -> {});
            var authorizationCalls = new AtomicInteger();
            var dnsCalls = new AtomicInteger();
            var probe =
                    probe(
                            LIMITS,
                            host -> {
                                dnsCalls.incrementAndGet();
                                return new InetAddress[] {InetAddress.getByName("127.0.0.1")};
                            });
            var base = target(fixture, "/live", null, null);
            var guarded =
                    new RtspDescribeProbe.Target(
                            base.uri(),
                            base.networkPolicyKey(),
                            null,
                            null,
                            () -> {
                                if (authorizationCalls.incrementAndGet() == revokedAt)
                                    throw new CameraAdapterException("ACTOR_REVOKED");
                            });
            var result = execute(probe, guarded);
            assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
            assertThat(result.reasonCode()).isEqualTo("ACTOR_REVOKED");
            assertThat(dnsCalls.get()).isEqualTo(revokedAt - 1);
            assertThat(fixture.acceptCount.get()).isZero();
        }
    }

    @Test
    void authorizationIsRecheckedBeforeAuthenticatedRetryAndDoesNotLeakUnknownErrors()
            throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        401,
                                        request.cseq(),
                                        Map.of("WWW-Authenticate", "Basic realm=\"fixture\""),
                                        new byte[0]));
        var authorizationCalls = new AtomicInteger();
        var base = target(fixture, "/live", "operator", "private");
        var guarded =
                new RtspDescribeProbe.Target(
                        base.uri(),
                        base.networkPolicyKey(),
                        base.username(),
                        base.password(),
                        () -> {
                            if (authorizationCalls.incrementAndGet() == 3)
                                throw new CameraAdapterException("CONFIGURATION_CHANGED");
                        });
        var result = execute(probe(LIMITS), guarded);
        assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.NOT_EXECUTED);
        assertThat(result.reasonCode()).isEqualTo("CONFIGURATION_CHANGED");
        assertThat(fixture.requests).hasSize(1);
        assertThat(authorizationCalls.get()).isEqualTo(3);
        var unsafe =
                new RtspDescribeProbe.Target(
                        base.uri(),
                        base.networkPolicyKey(),
                        base.username(),
                        base.password(),
                        () -> {
                            throw new CameraAdapterException("rtsp://operator:private@host/path");
                        });
        var unknown = execute(probe(LIMITS), unsafe);
        assertThat(unknown.reasonCode()).isEqualTo("AUTHORIZATION_UNAVAILABLE");
        assertThat(unknown.toString()).doesNotContain("private", "operator", "path");
    }

    @Test
    void slowHeaderBytesCannotResetTotalDeadline() throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) -> {
                            for (byte value :
                                    "RTSP/1.0 200 OK\r\n".getBytes(StandardCharsets.US_ASCII)) {
                                socket.getOutputStream().write(value);
                                socket.getOutputStream().flush();
                                Thread.sleep(30);
                            }
                        });
        var result =
                probe(LIMITS)
                        .execute(
                                target(fixture, "/live", null, null),
                                deadline(180),
                                new RtspDescribeProbe.Cancellation());
        assertThat(result.reasonCode()).isEqualTo("RTSP_TIMEOUT");
        assertThat(result.durationMs()).isLessThan(1500);
    }

    @Test
    void samePhysicalEndpointDifferentPathsNeverOpenSecondConcurrentSocket() throws Exception {
        var release = new CountDownLatch(1);
        var fixture =
                fixture(
                        (socket, request, index) -> {
                            release.await(2, TimeUnit.SECONDS);
                            reply(
                                    socket,
                                    200,
                                    request.cseq(),
                                    Map.of(),
                                    SDP.getBytes(StandardCharsets.UTF_8));
                        });
        var probe = probe(LIMITS);
        var first = calls.submit(() -> execute(probe, target(fixture, "/first", null, null)));
        assertThat(fixture.accepted.await(1, TimeUnit.SECONDS)).isTrue();
        try {
            var second = execute(probe, target(fixture, "/second", null, null));
            assertThat(second.reasonCode()).isEqualTo("ENDPOINT_BUSY");
            assertThat(fixture.acceptCount.get()).isEqualTo(1);
        } finally {
            release.countDown();
        }
        assertThat(first.get(2, TimeUnit.SECONDS).outcome())
                .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
    }

    @Test
    void cancellationBeforeExecutionOpensNoSocketAndReuseIsRejected() throws Exception {
        var fixture =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        200,
                                        request.cseq(),
                                        Map.of(),
                                        SDP.getBytes(StandardCharsets.UTF_8)));
        var cancellation = new RtspDescribeProbe.Cancellation();
        cancellation.cancel();
        var probe = probe(LIMITS);
        assertThat(
                        probe.execute(
                                        target(fixture, "/live", null, null),
                                        deadline(1000),
                                        cancellation)
                                .reasonCode())
                .isEqualTo("CANCELLED");
        assertThat(fixture.acceptCount.get()).isZero();
        var oneUse = new RtspDescribeProbe.Cancellation();
        assertThat(
                        probe.execute(target(fixture, "/live", null, null), deadline(1000), oneUse)
                                .outcome())
                .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
        assertThat(
                        probe.execute(target(fixture, "/live", null, null), deadline(1000), oneUse)
                                .reasonCode())
                .isEqualTo("CANCELLATION_IN_USE");
        assertThat(fixture.acceptCount.get()).isEqualTo(1);
    }

    @Test
    void cancellationAfterSocketRegistrationClosesConnectionPromptly() throws Exception {
        var peerClosed = new CountDownLatch(1);
        var fixture =
                fixture(
                        (socket, request, index) -> {
                            if (socket.getInputStream().read() == -1) peerClosed.countDown();
                        });
        var cancellation = new RtspDescribeProbe.Cancellation();
        var probe = probe(LIMITS);
        var result =
                calls.submit(
                        () ->
                                probe.execute(
                                        target(fixture, "/live", null, null),
                                        deadline(1500),
                                        cancellation));
        assertThat(fixture.accepted.await(1, TimeUnit.SECONDS)).isTrue();
        cancellation.cancel();
        assertThat(result.get(1, TimeUnit.SECONDS).reasonCode()).isEqualTo("CANCELLED");
        assertThat(peerClosed.await(1, TimeUnit.SECONDS)).isTrue();
    }

    @Test
    void dnsTimeoutKeepsSlotUntilResolverActuallyExitsAndNeverOpensLateSocket() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var resolved = new CountDownLatch(1);
        var count = new AtomicInteger();
        var fixture =
                fixture(
                        (socket, request, index) ->
                                reply(
                                        socket,
                                        200,
                                        request.cseq(),
                                        Map.of(),
                                        SDP.getBytes(StandardCharsets.UTF_8)));
        var probe =
                probe(
                        new RtspDescribeProbe.Limits(1, 1, 1, 8192, 16384, 2048, 8),
                        host -> {
                            if (count.incrementAndGet() == 1) {
                                entered.countDown();
                                try {
                                    awaitIgnoringInterrupt(release);
                                } finally {
                                    resolved.countDown();
                                }
                            }
                            return new InetAddress[] {InetAddress.getByName("127.0.0.1")};
                        });
        var first =
                calls.submit(
                        () ->
                                probe.execute(
                                        target(fixture, "/live", null, null),
                                        deadline(150),
                                        new RtspDescribeProbe.Cancellation()));
        assertThat(entered.await(1, TimeUnit.SECONDS)).isTrue();
        try {
            assertThat(first.get(1, TimeUnit.SECONDS).reasonCode()).isEqualTo("RTSP_TIMEOUT");
            assertThat(execute(probe, target(fixture, "/live", null, null)).reasonCode())
                    .isEqualTo("PROBE_BUSY");
            assertThat(fixture.acceptCount.get()).isZero();
        } finally {
            release.countDown();
        }
        assertThat(resolved.await(1, TimeUnit.SECONDS)).isTrue();
        assertThat(fixture.accepted.await(100, TimeUnit.MILLISECONDS)).isFalse();
        assertThat(execute(probe, target(fixture, "/live", null, null)).outcome())
                .isEqualTo(CameraProbeResult.Outcome.ACCESSIBLE);
        assertThat(fixture.acceptCount.get()).isEqualTo(1);
    }

    @Test
    void closeDoesNotClaimResolverHasStoppedAndRejectsFurtherExecution() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var fixture = fixture((socket, request, index) -> {});
        var probe =
                probe(
                        LIMITS,
                        host -> {
                            entered.countDown();
                            awaitIgnoringInterrupt(release);
                            return new InetAddress[] {InetAddress.getByName("127.0.0.1")};
                        });
        var running = calls.submit(() -> execute(probe, target(fixture, "/live", null, null)));
        assertThat(entered.await(1, TimeUnit.SECONDS)).isTrue();
        try {
            probe.close();
            assertThat(running.get(1, TimeUnit.SECONDS).reasonCode()).isEqualTo("CANCELLED");
            assertThat(probe.awaitStopped(Duration.ofMillis(30))).isFalse();
            assertThat(execute(probe, target(fixture, "/live", null, null)).reasonCode())
                    .isEqualTo("PROBE_CLOSED");
            assertThat(fixture.acceptCount.get()).isZero();
        } finally {
            release.countDown();
        }
        assertThat(probe.awaitStopped(Duration.ofSeconds(1))).isTrue();
    }

    @Test
    void closeDuringTargetValidationCancelsBeforeDeadlineRegistration() throws Exception {
        var fixture = fixture((socket, request, index) -> {});
        var resolverCalls = new AtomicInteger();
        var probe =
                probe(
                        LIMITS,
                        host -> {
                            resolverCalls.incrementAndGet();
                            return new InetAddress[] {InetAddress.getByName("127.0.0.1")};
                        });
        // Deterministically close after active registration but before timer.schedule.
        when(rules.host(anyString(), anyString()))
                .thenAnswer(
                        call -> {
                            probe.close();
                            return call.getArgument(0);
                        });
        var result = execute(probe, target(fixture, "/live", null, null));
        assertThat(result.outcome()).isEqualTo(CameraProbeResult.Outcome.CANCELLED);
        assertThat(result.reasonCode()).isEqualTo("CANCELLED");
        assertThat(resolverCalls.get()).isZero();
        assertThat(fixture.acceptCount.get()).isZero();
    }

    @Test
    void hostnameAndEachResolvedAddressAreAuthorizedBeforeConnecting() throws Exception {
        var fixture = fixture((socket, request, index) -> {});
        when(rules.host(eq("127.0.0.1"), anyString()))
                .thenThrow(new IllegalArgumentException("blocked"));
        var resolverCalls = new AtomicInteger();
        var probe =
                probe(
                        LIMITS,
                        host -> {
                            resolverCalls.incrementAndGet();
                            return new InetAddress[] {InetAddress.getByName("127.0.0.1")};
                        });
        assertThat(execute(probe, target(fixture, "/live", null, null)).reasonCode())
                .isEqualTo("NETWORK_BLOCKED");
        assertThat(resolverCalls.get()).isZero();
        var hostname =
                new RtspDescribeProbe.Target(
                        URI.create("rtsp://fixture.invalid:" + fixture.port() + "/live"),
                        "fixture",
                        null,
                        null);
        assertThat(execute(probe, hostname).reasonCode()).isEqualTo("NETWORK_BLOCKED");
        assertThat(resolverCalls.get()).isEqualTo(1);
        assertThat(fixture.acceptCount.get()).isZero();
    }

    private RtspDescribeProbe probe(RtspDescribeProbe.Limits limits) {
        return probe(limits, host -> new InetAddress[] {InetAddress.getByName("127.0.0.1")});
    }

    private RtspDescribeProbe probe(
            RtspDescribeProbe.Limits limits, RtspDescribeProbe.Resolver resolver) {
        var probe = new RtspDescribeProbe(rules, limits, resolver);
        probes.add(probe);
        return probe;
    }

    private Fixture fixture(Handler handler) throws IOException {
        var fixture = new Fixture(handler);
        fixtures.add(fixture);
        return fixture;
    }

    private static RtspDescribeProbe.Target target(
            Fixture fixture, String path, String username, String password) {
        return new RtspDescribeProbe.Target(
                URI.create("rtsp://127.0.0.1:" + fixture.port() + path),
                "fixture",
                username,
                password);
    }

    private static CameraProbeResult execute(
            RtspDescribeProbe probe, RtspDescribeProbe.Target target) {
        return probe.execute(target, deadline(1500), new RtspDescribeProbe.Cancellation());
    }

    private static long deadline(long millis) {
        return System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(millis);
    }

    private static String md5(String value) throws Exception {
        return HexFormat.of()
                .formatHex(
                        MessageDigest.getInstance("MD5")
                                .digest(value.getBytes(StandardCharsets.ISO_8859_1)));
    }

    private static void awaitIgnoringInterrupt(CountDownLatch latch) {
        boolean interrupted = false;
        while (latch.getCount() != 0) {
            try {
                latch.await();
            } catch (InterruptedException ex) {
                interrupted = true;
            }
        }
        if (interrupted) Thread.currentThread().interrupt();
    }

    private static void reply(
            Socket socket, int status, String sequence, Map<String, String> headers, byte[] body)
            throws IOException {
        var value =
                new StringBuilder("RTSP/1.0 " + status + " Fixture\r\nCSeq: " + sequence + "\r\n");
        headers.forEach(
                (key, entry) -> value.append(key).append(": ").append(entry).append("\r\n"));
        value.append("Content-Type: application/sdp\r\nContent-Length: ")
                .append(body.length)
                .append("\r\n\r\n");
        socket.getOutputStream().write(value.toString().getBytes(StandardCharsets.US_ASCII));
        socket.getOutputStream().write(body);
        socket.getOutputStream().flush();
    }

    private record Request(String line, Map<String, String> headers) {
        String cseq() {
            return headers.get("cseq");
        }
    }

    @FunctionalInterface
    private interface Handler {
        void handle(Socket socket, Request request, int index) throws Exception;
    }

    private static final class Fixture implements AutoCloseable {
        private final ServerSocket server;
        private final ExecutorService executor;
        private final Set<Socket> sockets = ConcurrentHashMap.newKeySet();
        private final List<Request> requests = new CopyOnWriteArrayList<>();
        private final AtomicInteger acceptCount = new AtomicInteger();
        private final CountDownLatch accepted = new CountDownLatch(1);

        Fixture(Handler handler) throws IOException {
            server = new ServerSocket();
            server.bind(new InetSocketAddress("127.0.0.1", 0));
            executor =
                    Executors.newSingleThreadExecutor(
                            task -> {
                                var thread = new Thread(task, "rtsp-socket-fixture");
                                thread.setDaemon(true);
                                return thread;
                            });
            executor.execute(
                    () -> {
                        while (!server.isClosed()) {
                            try (Socket socket = server.accept()) {
                                sockets.add(socket);
                                int index = acceptCount.incrementAndGet();
                                socket.setSoTimeout(2000);
                                var reader =
                                        new BufferedReader(
                                                new InputStreamReader(
                                                        socket.getInputStream(),
                                                        StandardCharsets.US_ASCII));
                                String line = reader.readLine();
                                if (line == null) continue;
                                Map<String, String> headers = new LinkedHashMap<>();
                                String header;
                                while ((header = reader.readLine()) != null && !header.isEmpty()) {
                                    int colon = header.indexOf(':');
                                    headers.put(
                                            header.substring(0, colon)
                                                    .toLowerCase(java.util.Locale.ROOT),
                                            header.substring(colon + 1).strip());
                                }
                                var request = new Request(line, headers);
                                requests.add(request);
                                accepted.countDown();
                                handler.handle(socket, request, index);
                            } catch (IOException ignored) {
                                // Cancellation/total deadline deliberately closes a peer during
                                // writes.
                            } catch (InterruptedException ex) {
                                Thread.currentThread().interrupt();
                                return;
                            } catch (Exception ex) {
                                throw new IllegalStateException("Fixture failed", ex);
                            } finally {
                                sockets.removeIf(Socket::isClosed);
                            }
                        }
                    });
        }

        int port() {
            return server.getLocalPort();
        }

        @Override
        public void close() throws Exception {
            server.close();
            for (Socket socket : sockets) socket.close();
            executor.shutdownNow();
            assertThat(executor.awaitTermination(2, TimeUnit.SECONDS)).isTrue();
        }
    }
}
