package com.streamfusion.platform.diagnostics;

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
        assertThat(result.truncated()).isTrue();
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
}
