package com.streamfusion.platform.monitor.security;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.json.JsonMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.monitor.diagnostics.DiagnosticLog;
import com.streamfusion.platform.system.auth.guard.ClientIpResolver;
import com.streamfusion.platform.system.auth.guard.IpGuardProperties;
import java.nio.file.*;
import java.time.*;
import java.util.List;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.mock.web.MockHttpServletRequest;

class SecurityMonitorTest {
    @TempDir Path directory;
    private final TestClock clock = new TestClock();
    private SecurityMonitor monitor;

    @BeforeEach
    void setup() {
        monitor = create(true);
        monitor.restore();
    }

    @AfterEach
    void close() {
        monitor.close();
    }

    private SecurityMonitor create(boolean enabled) {
        return new SecurityMonitor(
                new SecurityMonitorProperties(
                        enabled, Duration.ofMinutes(1), Duration.ofMinutes(1), 8, 10, 12, 5, 12),
                new PlatformProperties(directory, Duration.ofSeconds(15)),
                clock,
                new ClientIpResolver(
                        new IpGuardProperties(
                                true,
                                20,
                                Duration.ofMinutes(10),
                                120,
                                Duration.ofMinutes(1),
                                "test",
                                List.of())),
                JsonMapper.builder().findAndAddModules().build());
    }

    private void emit(String ip, String path, String code, int status, boolean known) {
        var request = new MockHttpServletRequest("GET", path);
        request.setRemoteAddr(ip);
        request.addHeader("X-Forwarded-For", "8.8.8.8");
        request.addHeader("Authorization", "secret-token");
        request.setQueryString("password=secret-query");
        request.setAttribute("traceId", "a".repeat(32));
        request.setAttribute(SecurityMonitor.ACTOR_ATTRIBUTE, 42L);
        request.setAttribute(DiagnosticLog.ERROR_CODE, code);
        monitor.observe(request, status, known);
    }

    private void login(int count) {
        for (int i = 0; i < count; i++)
            emit("192.0.2.1", "/auth/login/secure", "LOGIN_FAILED", 401, true);
        monitor.tick();
    }

    @Test
    void thresholdMergesDuringCooldownAndUsesTrustedIpAndSafeEvidence() throws Exception {
        login(7);
        assertThat(monitor.feed().retainedAlerts()).isZero();
        login(1);
        var first = monitor.page(1, 20).items().getFirst();
        assertThat(first.rule()).isEqualTo(SecurityAlert.Rule.LOGIN_FAILURE_BURST);
        assertThat(first.sourceIp()).isEqualTo("192.0.2.1");
        assertThat(first.actorIds()).containsExactly("42");
        login(4);
        var merged = monitor.page(1, 20).items().getFirst();
        assertThat(merged.id()).isEqualTo(first.id());
        assertThat(merged.eventCount()).isEqualTo(12);
        assertThat(monitor.feed().revision()).isEqualTo(1);
        String serialized =
                JsonMapper.builder().findAndAddModules().build().writeValueAsString(merged);
        assertThat(serialized)
                .doesNotContain("secret", "8.8.8.8", "/auth/login", "password", "Authorization");
        clock.advance(61);
        login(8);
        assertThat(monitor.feed().revision()).isEqualTo(2);
    }

    @Test
    void normalSessionProbesExpiredSessionsAndServerErrorsAreNotAttacks() {
        for (int i = 0; i < 20; i++) {
            emit("192.0.2.1", "/auth/session", "UNAUTHORIZED", 401, true);
            emit("192.0.2.1", "/auth/me", "UNAUTHORIZED", 401, true);
            emit("192.0.2.1", "/user/page", "SESSION_REPLACED", 401, true);
            emit("192.0.2.1", "/user/page", "PASSWORD_CHANGE_REQUIRED", 403, true);
            emit("192.0.2.1", "/user/page", "INTERNAL_ERROR", 500, true);
        }
        monitor.tick();
        assertThat(monitor.feed().retainedAlerts()).isZero();
    }

    @Test
    void correlatesThreeDifferentSignalsBelowTheirIndividualThresholds() {
        for (int i = 0; i < 4; i++) {
            emit("192.0.2.1", "/auth/login/secure", "CAPTCHA_INVALID", 400, true);
            emit("192.0.2.1", "/user/page", "FORBIDDEN", 403, true);
            emit("192.0.2.1", "/roles/1", "CSRF_INVALID", 403, true);
        }
        monitor.tick();
        var items = monitor.page(1, 20).items();
        assertThat(items).hasSize(1);
        assertThat(items.getFirst().rule()).isEqualTo(SecurityAlert.Rule.MIXED_PROBING);
        assertThat(items.getFirst().categories()).hasSize(3);
    }

    @Test
    void pathScanNeedsDiversityAndNeverReturnsRawPaths() {
        for (int i = 0; i < 20; i++)
            emit("192.0.2.1", "/missing-secret", "UNAUTHORIZED", 401, false);
        monitor.tick();
        assertThat(monitor.feed().retainedAlerts()).isZero();
        for (int i = 0; i < 8; i++)
            emit("192.0.2.1", "/private-secret-" + i, "UNAUTHORIZED", 401, false);
        monitor.tick();
        var alert = monitor.page(1, 20).items().getFirst();
        assertThat(alert.rule()).isEqualTo(SecurityAlert.Rule.PATH_SCAN);
        assertThat(alert.distinctPaths()).isEqualTo(9);
        assertThat(alert.toString()).doesNotContain("secret");
    }

    @Test
    void separateSourcesAndExpiredWindowDoNotAccumulate() {
        login(7);
        clock.advance(61);
        login(1);
        for (int i = 0; i < 7; i++)
            emit("192.0.2.2", "/auth/login/secure", "LOGIN_FAILED", 401, true);
        monitor.tick();
        assertThat(monitor.feed().retainedAlerts()).isZero();
    }

    @Test
    void queueAndPerSourceWindowsAreBoundedAndReportLostCoverage() {
        for (int i = 0; i < 1100; i++) emit("192.0.2.1", "/user/page", "FORBIDDEN", 403, true);
        assertThat(monitor.feed().droppedEvents()).isEqualTo(76);
        for (int i = 0; i < 4; i++) monitor.tick();
        assertThat(monitor.feed().truncatedWindows()).isEqualTo(896);
        assertThat(monitor.page(1, 20).items().getFirst().eventCount()).isEqualTo(128);
    }

    @Test
    void sourceCardinalityIsBounded() {
        for (int i = 0; i < 2100; i++) {
            emit("198.18." + (i / 256) + "." + (i % 256), "/user/page", "FORBIDDEN", 403, true);
            if (i % 200 == 0) monitor.tick();
        }
        monitor.tick();
        assertThat(monitor.feed().evictedSources()).isEqualTo(52);
    }

    @Test
    void snapshotSurvivesRestartAndExpiresWithoutTouchingBusinessData() {
        login(8);
        String id = monitor.page(1, 20).items().getFirst().id();
        monitor.close();
        monitor = create(true);
        monitor.restore();
        assertThat(monitor.page(1, 20).items().getFirst().id()).isEqualTo(id);
        assertThat(monitor.feed().revision()).isZero();
        clock.advance(7 * 86400 + 1);
        monitor.tick();
        assertThat(monitor.page(1, 20).items()).isEmpty();
    }

    @Test
    void corruptSnapshotIsPreservedAndFailureIsVisible() throws Exception {
        monitor.close();
        Path file = directory.resolve("security/alerts.json");
        Files.writeString(file, "not-json");
        monitor = create(true);
        monitor.restore();
        login(8);
        assertThat(monitor.feed().persistenceHealthy()).isFalse();
        assertThat(monitor.feed().retainedAlerts()).isEqualTo(1);
        assertThat(Files.readString(file)).isEqualTo("not-json");
    }

    @Test
    void anotherInstanceCannotOverwriteOwnedWorkDirectory() {
        var second = create(true);
        try {
            second.restore();
            assertThat(second.feed().persistenceHealthy()).isFalse();
            assertThat(monitor.feed().persistenceHealthy()).isTrue();
        } finally {
            second.close();
        }
    }

    @Test
    void disabledMonitorDoesNotCollect() {
        monitor.close();
        monitor = create(false);
        login(20);
        assertThat(monitor.feed().enabled()).isFalse();
        assertThat(monitor.page(1, 20).items()).isEmpty();
    }

    @Test
    void retainedAlertsAndSnapshotSizeRemainBoundedUnderManySources() throws Exception {
        for (int i = 0; i < 1002; i++) {
            for (int n = 0; n < 8; n++)
                emit(
                        "198.18." + (i / 256) + "." + (i % 256),
                        "/auth/login/secure",
                        "LOGIN_FAILED",
                        401,
                        true);
            if (i % 20 == 0) monitor.tick();
        }
        monitor.tick();
        clock.advance(6);
        monitor.tick();
        assertThat(monitor.feed().retainedAlerts()).isEqualTo(1000);
        assertThat(monitor.page(1, 100).total()).isEqualTo(1000);
        assertThat(monitor.feed().persistenceHealthy()).isTrue();
        assertThat(Files.size(directory.resolve("security/alerts.json")))
                .isLessThan(2 * 1024 * 1024);
    }

    @Test
    void diskWriteFailureKeepsMemoryEvidenceAndDoesNotAccumulateTemporaryFiles() throws Exception {
        Path occupied = Files.createDirectory(directory.resolve("security/alerts.json"));
        Files.writeString(occupied.resolve("keep"), "do-not-delete");
        for (int attempt = 0; attempt < 3; attempt++) {
            login(8);
            clock.advance(6);
        }
        assertThat(monitor.feed().persistenceHealthy()).isFalse();
        assertThat(monitor.feed().retainedAlerts()).isEqualTo(1);
        assertThat(Files.readString(occupied.resolve("keep"))).isEqualTo("do-not-delete");
        try (var files = Files.list(directory.resolve("security"))) {
            assertThat(files.map(p -> p.getFileName().toString()).toList())
                    .allMatch(
                            name ->
                                    List.of("alerts.lock", "alerts.json", "alerts.pending")
                                            .contains(name));
        }
    }

    private static final class TestClock extends Clock {
        private Instant now = Instant.parse("2026-10-22T00:00:00Z");

        void advance(long seconds) {
            now = now.plusSeconds(seconds);
        }

        @Override
        public ZoneId getZone() {
            return ZoneOffset.UTC;
        }

        @Override
        public Clock withZone(ZoneId zone) {
            return this;
        }

        @Override
        public Instant instant() {
            return now;
        }
    }
}
