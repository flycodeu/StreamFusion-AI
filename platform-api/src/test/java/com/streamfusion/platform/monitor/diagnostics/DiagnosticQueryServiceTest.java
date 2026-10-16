package com.streamfusion.platform.monitor.diagnostics;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.common.exception.BusinessException;
import java.nio.file.*;
import java.time.*;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class DiagnosticQueryServiceTest {
    @TempDir Path work;
    final ObjectMapper json = new ObjectMapper().findAndRegisterModules();
    final Instant now = Instant.parse("2026-10-14T12:00:00Z");
    DiagnosticQueryService service;
    Path logs;

    @BeforeEach
    void prepare() throws Exception {
        logs = Files.createDirectory(work.resolve("logs"));
        service =
                new DiagnosticQueryService(
                        new PlatformProperties(work, Duration.ofSeconds(15)),
                        json,
                        Clock.fixed(now, ZoneOffset.UTC));
    }

    DiagnosticEvent event(String trace, int seconds) {
        return DiagnosticEvent.builder()
                .occurredAt(now.minusSeconds(seconds))
                .kind("REQUEST")
                .level("WARN")
                .traceId(trace)
                .method("POST")
                .route("/cameras/{id}")
                .status(409)
                .durationMs(12L)
                .errorCode("CONFLICT")
                .stack(List.of())
                .build();
    }

    DiagnosticQueryService.Query query(String trace) {
        return new DiagnosticQueryService.Query(null, null, trace, null, null, null, null);
    }

    @Test
    void newestRecordsAcrossRotationAndPartialWrites() throws Exception {
        String trace = "a".repeat(32);
        Files.writeString(
                logs.resolve("diagnostics.jsonl"),
                json.writeValueAsString(event(trace, 1)) + "\n{partial");
        Files.writeString(
                logs.resolve("diagnostics.2026-10-14.1.jsonl"),
                "broken\n" + json.writeValueAsString(event(trace, 2)) + "\n");
        Files.writeString(
                logs.resolve("other.jsonl"), json.writeValueAsString(event(trace, 0)) + "\n");
        var result = service.query(query(trace));
        assertThat(result.items())
                .extracting(DiagnosticEvent::occurredAt)
                .containsExactly(now.minusSeconds(1), now.minusSeconds(2));
        assertThat(result.unavailable()).isTrue();
        assertThat(result.truncated()).isFalse();
        assertThat(service.query(query("b".repeat(32))).items()).isEmpty();
    }

    @Test
    void capsResultCountAndScannedBytes() throws Exception {
        String line = json.writeValueAsString(event("a".repeat(32), 1)) + "\n";
        Files.writeString(logs.resolve("diagnostics.jsonl"), line.repeat(201));
        var result = service.query(query(null));
        assertThat(result.items()).hasSize(200);
        assertThat(result.truncated()).isFalse();
        assertThat(result.nextCursor()).isNotNull();
        Files.writeString(
                logs.resolve("diagnostics.jsonl"), "x".repeat(17 * 1024 * 1024) + "\n" + line);
        assertThat(service.query(query(null)).truncated()).isTrue();
        assertThat(service.query(query(null)).items()).hasSize(1);
    }

    @Test
    void validatesTimeRangeAndIdentifiers() {
        assertThatThrownBy(() -> service.query(query("../../etc")))
                .isInstanceOf(BusinessException.class);
        assertThatThrownBy(
                        () ->
                                service.query(
                                        new DiagnosticQueryService.Query(
                                                now.minus(Duration.ofDays(8)),
                                                now,
                                                null,
                                                null,
                                                null,
                                                null,
                                                null)))
                .isInstanceOf(BusinessException.class);
        assertThatThrownBy(
                        () ->
                                service.query(
                                        new DiagnosticQueryService.Query(
                                                null, null, null, "-1", null, null, null)))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void excludesOutsideTimeRangeAndFiltersTaskRecords() throws Exception {
        var task =
                DiagnosticEvent.builder()
                        .occurredAt(now)
                        .kind("CAMERA_CHECK")
                        .level("INFO")
                        .planId("91")
                        .checkId("92")
                        .state("COMPLETED")
                        .result("ACCESSIBLE")
                        .stack(List.of())
                        .build();
        Files.writeString(
                logs.resolve("diagnostics.jsonl"),
                json.writeValueAsString(event("a".repeat(32), 90000))
                        + "\n"
                        + json.writeValueAsString(task)
                        + "\n");
        var query =
                new DiagnosticQueryService.Query(
                        null, null, null, "91", "92", "CAMERA_CHECK", "INFO");
        assertThat(service.query(query).items()).containsExactly(task);
    }

    private DiagnosticQueryService.Query page(String cursor, int size) {
        return new DiagnosticQueryService.Query(
                now.minusSeconds(86400), now, null, null, null, null, null, size, cursor);
    }

    @Test
    void pagesIdenticalTimestampsAcrossRotationAndConcurrentAppend() throws Exception {
        var path = logs.resolve("diagnostics.jsonl");
        var lines = new StringBuilder();
        for (int i = 1; i <= 121; i++)
            lines.append(json.writeValueAsString(event(String.format("%032x", i), 1))).append('\n');
        Files.writeString(path, lines);
        var first = service.query(page(null, 50));
        assertThat(first.items()).hasSize(50);
        assertThat(first.truncated()).isFalse();
        assertThat(first.nextCursor()).isNotNull();
        Files.move(path, logs.resolve("diagnostics.2026-10-14.0.jsonl"));
        Files.writeString(path, json.writeValueAsString(event("f".repeat(32), 1)) + "\n");
        var second = service.query(page(first.nextCursor(), 50));
        assertThat(second.cursorExpired())
                .as("second page cursor %s", first.nextCursor())
                .isFalse();
        assertThat(second.items()).hasSize(50);
        var third = service.query(page(second.nextCursor(), 50));
        var ids =
                java.util.stream.Stream.of(first, second, third)
                        .flatMap(r -> r.items().stream())
                        .map(DiagnosticEvent::traceId)
                        .toList();
        assertThat(ids).hasSize(121).doesNotHaveDuplicates().doesNotContain("f".repeat(32));
        assertThat(third.nextCursor()).isNull();
        assertThat(service.query(page(first.nextCursor(), 50)).items()).isEqualTo(second.items());
    }

    @Test
    void expiresDeletedOrTruncatedCursorAndRejectsMalformedInputs() throws Exception {
        var path = logs.resolve("diagnostics.jsonl");
        Files.writeString(
                path, (json.writeValueAsString(event("a".repeat(32), 1)) + "\n").repeat(3));
        var first = service.query(page(null, 1));
        Files.writeString(path, "");
        assertThat(service.query(page(first.nextCursor(), 1)).cursorExpired()).isTrue();
        Files.delete(path);
        assertThat(service.query(page(first.nextCursor(), 1)).cursorExpired()).isTrue();
        assertThatThrownBy(() -> service.query(page("../secret", 50)))
                .isInstanceOf(BusinessException.class);
        assertThatThrownBy(() -> service.query(page(null, 201)))
                .isInstanceOf(BusinessException.class);
        assertThatThrownBy(() -> service.query(page(null, 0)))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void scansAcrossBlockBoundariesWithoutDroppingUtf8Records() throws Exception {
        var path = logs.resolve("diagnostics.jsonl");
        var lines = new StringBuilder();
        for (int i = 1; i <= 3100; i++)
            lines.append(json.writeValueAsString(event(String.format("%032x", i), 1))).append('\n');
        Files.writeString(path, lines);
        String cursor = null;
        var ids = new java.util.HashSet<String>();
        int count = 0;
        do {
            var result = service.query(page(cursor, 200));
            assertThat(result.unavailable()).isFalse();
            count += result.items().size();
            result.items().forEach(e -> ids.add(e.traceId()));
            cursor = result.nextCursor();
            assertThat(count).isLessThanOrEqualTo(3100);
        } while (cursor != null);
        assertThat(count).isEqualTo(3100);
        assertThat(ids).hasSize(3100);
    }

    @Test
    void sparseFilterContinuesAcrossReadBlocks() throws Exception {
        var older = event("a".repeat(32), 1);
        String ignored =
                json.writeValueAsString(
                                DiagnosticEvent.builder()
                                        .occurredAt(now)
                                        .kind("REQUEST")
                                        .level("INFO")
                                        .route("/相机/状态")
                                        .stack(List.of())
                                        .build())
                        + "\n";
        Files.writeString(
                logs.resolve("diagnostics.jsonl"),
                json.writeValueAsString(older) + "\n" + ignored.repeat(6000));
        var result =
                service.query(
                        new DiagnosticQueryService.Query(
                                null, null, null, null, null, null, "WARN", 50, null));
        assertThat(result.items()).containsExactly(older);
        assertThat(result.unavailable()).isFalse();
        assertThat(result.truncated()).isFalse();
        assertThat(result.nextCursor()).isNull();
    }
}
