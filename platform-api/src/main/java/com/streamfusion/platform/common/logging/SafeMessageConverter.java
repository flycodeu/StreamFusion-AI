package com.streamfusion.platform.common.logging;

import ch.qos.logback.classic.pattern.ClassicConverter;
import ch.qos.logback.classic.spi.ILoggingEvent;

public class SafeMessageConverter extends ClassicConverter {
    @Override
    public String convert(ILoggingEvent event) {
        return LogRedactor.redact(event.getFormattedMessage())
                .replace('\n', ' ')
                .replace('\r', ' ');
    }
}
