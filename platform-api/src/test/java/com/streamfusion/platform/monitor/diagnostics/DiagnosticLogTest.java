package com.streamfusion.platform.monitor.diagnostics;

import static org.assertj.core.api.Assertions.*;

import ch.qos.logback.classic.Logger;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.core.read.ListAppender;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.time.Clock;
import org.junit.jupiter.api.Test;
import org.slf4j.LoggerFactory;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.web.servlet.HandlerMapping;

class DiagnosticLogTest {
    @Test
    void excludesCredentialsRawUrlBodyAndExceptionMessage() throws Exception {
        var logger = (Logger) LoggerFactory.getLogger("diagnostic.events");
        var capture = new ListAppender<ILoggingEvent>();
        capture.start();
        logger.addAppender(capture);
        try {
            var json = new ObjectMapper().findAndRegisterModules();
            var log =
                    new DiagnosticLog(
                            json,
                            Clock.systemUTC(),
                            org.mockito.Mockito.mock(
                                    org.springframework.beans.factory.ObjectProvider.class));
            var request = new MockHttpServletRequest("POST", "/camera/password-secret");
            request.addHeader("Authorization", "Bearer token-secret");
            request.setQueryString("password=query-secret");
            request.setContent("body-secret".getBytes());
            request.setAttribute("traceId", "a".repeat(32));
            request.setAttribute(HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE, "/camera/{id}");
            request.setAttribute(
                    DiagnosticLog.EXCEPTION,
                    new RuntimeException("rtsp://user:exception-secret@host/path"));
            log.request(request, 500, 12);
            String output = capture.list.getFirst().getFormattedMessage();
            assertThat(output)
                    .contains("/camera/{id}", "java.lang.RuntimeException")
                    .doesNotContain("secret", "Bearer", "rtsp://", "Authorization");
            assertThat(json.readTree(output).path("stack").size()).isBetween(1, 20);
            TransactionSynchronizationManager.initSynchronization();
            try {
                log.check(
                        "a".repeat(32),
                        1L,
                        2L,
                        3L,
                        4L,
                        "COMPLETED",
                        "ACCESSIBLE",
                        "RTSP_ACCESSIBLE",
                        9L);
                assertThat(capture.list).hasSize(1);
                TransactionSynchronizationManager.getSynchronizations()
                        .forEach(s -> s.afterCommit());
                assertThat(capture.list).hasSize(2);
            } finally {
                TransactionSynchronizationManager.clearSynchronization();
            }
        } finally {
            logger.detachAppender(capture);
            capture.stop();
        }
    }
}
