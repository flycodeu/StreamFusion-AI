package com.streamfusion.platform.common.logging;

import ch.qos.logback.classic.pattern.ThrowableHandlingConverter;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.classic.spi.ThrowableProxyUtil;

public class SafeExceptionConverter extends ThrowableHandlingConverter {
    @Override
    public String convert(ILoggingEvent event) {
        return event.getThrowableProxy() == null
                ? ""
                : LogRedactor.redact(ThrowableProxyUtil.asString(event.getThrowableProxy()));
    }
}
