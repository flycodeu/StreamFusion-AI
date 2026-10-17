package com.streamfusion.platform.common.logging;

import static org.assertj.core.api.Assertions.assertThat;

import ch.qos.logback.classic.LoggerContext;
import ch.qos.logback.classic.PatternLayout;
import ch.qos.logback.classic.spi.LoggingEvent;
import ch.qos.logback.classic.spi.ThrowableProxy;
import org.junit.jupiter.api.Test;

class LogRedactorTest {
    @Test
    void hidesEscapedQuotesAndBackslashesWithoutRemovingFollowingFields() {
        String json = "{\"password\": \"head\\\"tail-secret\\\\ending\", \"status\": \"ok\"}";
        String exception = "{'token': 'head\\'tail-secret', 'status': 'ok'}";

        assertThat(LogRedactor.redact(json))
                .isEqualTo("{\"password\": [REDACTED], \"status\": \"ok\"}");
        assertThat(LogRedactor.redact(exception))
                .isEqualTo("{'token': [REDACTED], 'status': 'ok'}");
    }

    @Test
    void hidesUnterminatedQuotedValuesIncludingDanglingEscapes() {
        for (String value :
                new String[] {
                    "password=\"head\\\"tail-secret with spaces",
                    "token='head\\'tail-secret with spaces\\",
                    "password=\"",
                    "token='\\"
                }) {
            assertThat(LogRedactor.redact(value))
                    .endsWith("[REDACTED]")
                    .doesNotContain("tail-secret", "with spaces");
        }
    }

    @Test
    void quotedSecretsCannotLoseEscapeBoundariesDuringBearerReplacement() {
        for (String value :
                new String[] {
                    "authorization=\"Bearer head\\\"tail-secret\" status=ok",
                    "password=\"Bearer head\\\"tail-secret\" status=ok",
                    "token='Bearer head\\'tail-secret' status=ok"
                }) {
            assertThat(LogRedactor.redact(value))
                    .endsWith("[REDACTED] status=ok")
                    .doesNotContain("tail-secret");
        }
        assertThat(LogRedactor.redact("authorization=Bearer raw-secret status=ok"))
                .doesNotContain("raw-secret")
                .endsWith("status=ok");
    }

    @Test
    void preservesExistingUrlBearerAndUnquotedValueBoundaries() {
        assertThat(
                        LogRedactor.redact(
                                "password=first;status=ok token=second&count=2 rtsp://alice:private@camera/live Bearer third"))
                .isEqualTo(
                        "password=[REDACTED];status=ok token=[REDACTED]&count=2 rtsp://[REDACTED]@camera/live Bearer [REDACTED]");
        assertThat(LogRedactor.redact("status=ok path=/health"))
                .isEqualTo("status=ok path=/health");
    }

    @Test
    void scansLongEscapedValuesWithoutRecursiveRegexBacktracking() {
        String value = "password=\"" + "\\\"".repeat(20000) + "tail-secret\" status=ok";
        assertThat(LogRedactor.redact(value)).isEqualTo("password=[REDACTED] status=ok");
        assertThat(LogRedactor.redact("x".repeat(20000))).hasSize(16384);
    }

    @Test
    void exceptionAndMessageConvertersUseTheSameQuotedValueBoundary() {
        var context = new LoggerContext();
        try {
            var layout = new PatternLayout();
            layout.setContext(context);
            layout.getInstanceConverterMap().put("safeMessage", SafeMessageConverter::new);
            layout.getInstanceConverterMap().put("safeException", SafeExceptionConverter::new);
            layout.setPattern("%safeMessage%n%safeException");
            layout.start();
            var event = new LoggingEvent();
            event.setMessage("password=\"head\\\"message-tail\" status=ok");
            event.setThrowableProxy(
                    new ThrowableProxy(new IllegalStateException("token='head\\'exception-tail'")));
            assertThat(layout.doLayout(event))
                    .contains("[REDACTED]", "status=ok", "IllegalStateException")
                    .doesNotContain("message-tail", "exception-tail");
            layout.stop();
        } finally {
            context.stop();
        }
    }
}
