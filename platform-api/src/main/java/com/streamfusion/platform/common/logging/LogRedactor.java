package com.streamfusion.platform.common.logging;

import java.util.regex.Pattern;

public final class LogRedactor {
    private static final Pattern URL = Pattern.compile("(?i)([a-z][a-z0-9+.-]*://)[^/\\s@]+@");
    private static final Pattern BEARER = Pattern.compile("(?i)\\bBearer\\s+[^\\s,;\"']+");
    private static final Pattern SECRET =
            Pattern.compile(
                    "(?i)([\"']?(?:password|passwd|token|secret|api[_-]?key|access[_-]?key|access[_-]?token|authorization)[\"']?\\s*[:=]\\s*)(?:\"[^\"]*\"|'[^']*'|[^\\s,;&}]+)");

    private LogRedactor() {}

    public static String redact(String value) {
        String result = URL.matcher(value).replaceAll("$1[REDACTED]@");
        result = BEARER.matcher(result).replaceAll("Bearer [REDACTED]");
        result = SECRET.matcher(result).replaceAll("$1[REDACTED]");
        return result.substring(0, Math.min(result.length(), 16384));
    }
}
