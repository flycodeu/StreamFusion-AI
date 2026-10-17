package com.streamfusion.platform.common.logging;

import java.util.regex.Pattern;

public final class LogRedactor {
    private static final Pattern URL = Pattern.compile("(?i)([a-z][a-z0-9+.-]*://)[^/\\s@]+@");
    private static final Pattern BEARER = Pattern.compile("(?i)\\bBearer\\s+[^\\s,;\"']+");
    private static final Pattern SECRET =
            Pattern.compile(
                    "(?i)[\"']?(?:captcha[_-]?answer|password|passwd|token|secret|api[_-]?key|access[_-]?key|access[_-]?token|authorization)[\"']?\\s*[:=]\\s*");

    private LogRedactor() {}

    public static String redact(String value) {
        // Redact quoted values before transport substitutions can remove an escape character.
        String result = redactSecrets(value, true);
        result = URL.matcher(result).replaceAll("$1[REDACTED]@");
        result = BEARER.matcher(result).replaceAll("Bearer [REDACTED]");
        result = redactSecrets(result, false);
        return result.substring(0, Math.min(result.length(), 16384));
    }

    private static String redactSecrets(String value, boolean quotedOnly) {
        var matches = SECRET.matcher(value);
        var safe = new StringBuilder();
        int copied = 0;
        while (matches.find(copied)) {
            int start = matches.end();
            safe.append(value, copied, start);
            if (quotedOnly && (start == value.length() || "\"'".indexOf(value.charAt(start)) < 0)) {
                copied = start;
                continue;
            }
            safe.append("[REDACTED]");
            copied = secretEnd(value, start);
            if (copied == value.length()) break;
        }
        return safe.append(value, copied, value.length()).toString();
    }

    /** Quoted exception/JSON values can contain escaped quotes or end mid-value. */
    private static int secretEnd(String value, int start) {
        if (start == value.length()) return start;
        char quote = value.charAt(start);
        if (quote == '\"' || quote == '\'') {
            for (int index = start + 1; index < value.length(); index++) {
                char current = value.charAt(index);
                if (current == '\\') index++;
                else if (current == quote) return index + 1;
            }
            return value.length();
        }
        int end = start;
        while (end < value.length() && " \t\n\r\f\u000b,;&}".indexOf(value.charAt(end)) < 0) end++;
        return end;
    }
}
