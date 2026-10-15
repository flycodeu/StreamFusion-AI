package com.streamfusion.platform.diagnostics;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.servlet.http.HttpServletRequest;
import java.time.Clock;
import java.util.Arrays;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Component;
import org.springframework.transaction.support.TransactionSynchronization;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.web.servlet.HandlerMapping;

@Component
@RequiredArgsConstructor
public class DiagnosticLog {
    public static final String ERROR_CODE = DiagnosticLog.class.getName() + ".code";
    public static final String REASON = DiagnosticLog.class.getName() + ".reason";
    public static final String EXCEPTION = DiagnosticLog.class.getName() + ".exception";
    private static final Logger EVENTS = LoggerFactory.getLogger("diagnostic.events");
    private static final Logger LOG = LoggerFactory.getLogger(DiagnosticLog.class);
    private final ObjectMapper json;
    private final Clock clock;
    private final org.springframework.beans.factory.ObjectProvider<DiagnosticPayloadAdvice>
            payloads;

    public void request(HttpServletRequest request, int status, long durationMs) {
        Object route = request.getAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE);
        Throwable failure = request.getAttribute(EXCEPTION) instanceof Throwable ex ? ex : null;
        Throwable root = failure;
        for (int depth = 0;
                root != null && root.getCause() != null && root.getCause() != root && depth < 5;
                depth++) root = root.getCause();
        var payload = payloads.getIfAvailable();
        write(
                DiagnosticEvent.builder()
                        .occurredAt(clock.instant())
                        .kind("REQUEST")
                        .level(status >= 500 ? "ERROR" : status >= 400 ? "WARN" : "INFO")
                        .traceId((String) request.getAttribute("traceId"))
                        .method(code(request.getMethod()))
                        .route(route instanceof String pattern ? pattern : "<unmatched>")
                        .status(status)
                        .durationMs(Math.max(0, durationMs))
                        .errorCode(code(request.getAttribute(ERROR_CODE)))
                        .request(payload == null ? null : payload.requestDetails(request, status))
                        .response(payload == null ? null : payload.responseDetails(request, status))
                        .reasonCode(code(request.getAttribute(REASON)))
                        .exceptionType(failure == null ? null : failure.getClass().getName())
                        .rootExceptionType(root == null ? null : root.getClass().getName())
                        .stack(
                                failure == null
                                        ? List.of()
                                        : Arrays.stream(root.getStackTrace())
                                                .limit(20)
                                                .map(StackTraceElement::toString)
                                                .toList())
                        .build());
    }

    public void check(
            String traceId,
            Long planId,
            long checkId,
            long cameraId,
            long profileId,
            String state,
            String result,
            String reason,
            Long durationMs) {
        var event =
                DiagnosticEvent.builder()
                        .occurredAt(clock.instant())
                        .kind("CAMERA_CHECK")
                        .level(
                                "ACCESSIBLE".equals(result)
                                                || "QUEUED".equals(state)
                                                || "RUNNING".equals(state)
                                        ? "INFO"
                                        : "WARN")
                        .traceId(traceId)
                        .planId(planId == null ? null : planId.toString())
                        .checkId(Long.toString(checkId))
                        .cameraId(Long.toString(cameraId))
                        .profileId(Long.toString(profileId))
                        .state(code(state))
                        .result(code(result))
                        .reasonCode(code(reason))
                        .durationMs(durationMs)
                        .stack(List.of())
                        .build();
        // A rolled-back status change must not produce a successful diagnostic event.
        if (TransactionSynchronizationManager.isSynchronizationActive()) {
            TransactionSynchronizationManager.registerSynchronization(
                    new TransactionSynchronization() {
                        @Override
                        public void afterCommit() {
                            write(event);
                        }
                    });
        } else write(event);
    }

    private void write(DiagnosticEvent event) {
        try {
            EVENTS.info(json.writeValueAsString(event));
        } catch (JsonProcessingException | RuntimeException ex) {
            LOG.warn("Diagnostic write failed traceId={}", event.traceId());
        }
    }

    private static String code(Object value) {
        return value instanceof String text && text.matches("[A-Z][A-Z0-9_]{0,79}") ? text : null;
    }
}
