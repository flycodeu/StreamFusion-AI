package com.streamfusion.platform.monitor.diagnostics;

import java.time.Instant;
import java.util.List;
import lombok.Builder;

/** Explicit safe fields only; request bodies, URLs, headers and exception messages are excluded. */
@Builder
public record DiagnosticEvent(
        Instant occurredAt,
        String kind,
        String level,
        String traceId,
        String method,
        String route,
        Integer status,
        Long durationMs,
        String errorCode,
        String reasonCode,
        String planId,
        String checkId,
        String cameraId,
        String profileId,
        String state,
        String result,
        String exceptionType,
        List<String> stack,
        com.fasterxml.jackson.databind.JsonNode request,
        com.fasterxml.jackson.databind.JsonNode response,
        String rootExceptionType) {
    public DiagnosticEvent {
        if (request != null && request.isNull()) request = null;
        if (response != null && response.isNull()) response = null;
    }
}
