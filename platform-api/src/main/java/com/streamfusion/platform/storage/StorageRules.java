package com.streamfusion.platform.storage;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.UUID;

public final class StorageRules {
    private StorageRules() {}

    public static LocalDateTime now() {
        return LocalDateTime.now(ZoneId.of("Asia/Shanghai"));
    }

    public static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }

    public static String managementUrl(String value) {
        if (value == null || value.isBlank()) return null;
        try {
            String url = text(value, 2048, true);
            URI uri = URI.create(url);
            if (!java.util.Set.of("http", "https").contains(uri.getScheme())
                    || uri.getHost() == null
                    || uri.getRawUserInfo() != null
                    || uri.getRawQuery() != null
                    || uri.getRawFragment() != null
                    || uri.getPort() != -1 && (uri.getPort() < 1 || uri.getPort() > 65535)
                    || url.codePoints().anyMatch(Character::isISOControl)) throw invalid();
            return uri.toASCIIString();
        } catch (IllegalArgumentException ex) {
            throw invalid();
        }
    }

    public static String text(String value, int max, boolean required) {
        return CameraSourceRules.text(value, max, required);
    }

    public static URI endpoint(String value) {
        try {
            URI uri = URI.create(text(value, 2048, true));
            if (!java.util.Set.of("http", "https").contains(uri.getScheme())
                    || uri.getHost() == null
                    || uri.getPort() != -1 && (uri.getPort() < 1 || uri.getPort() > 65535)
                    || uri.getRawUserInfo() != null
                    || uri.getRawQuery() != null
                    || uri.getRawFragment() != null
                    || !(uri.getRawPath().isEmpty() || uri.getRawPath().equals("/")))
                throw invalid();
            return URI.create(uri.getScheme() + "://" + uri.getRawAuthority());
        } catch (IllegalArgumentException ex) {
            throw invalid();
        }
    }

    public static String bucket(String value) {
        if (value == null
                || !value.matches("[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]")
                || value.contains("..")
                || value.matches("[0-9]+(?:\\.[0-9]+){3}")) throw invalid();
        return value;
    }

    public static String prefix(String value) {
        value = value == null ? "" : value;
        key(value, true);
        if (!value.isEmpty() && (!value.endsWith("/") || value.startsWith("/"))) throw invalid();
        return value;
    }

    public static String key(String value, boolean emptyAllowed) {
        if (value == null
                || (!emptyAllowed && value.isEmpty())
                || value.getBytes(StandardCharsets.UTF_8).length > 1024
                || value.codePoints().anyMatch(Character::isISOControl)) throw invalid();
        return value;
    }

    public static String requestKey(String value) {
        try {
            return UUID.fromString(value).toString();
        } catch (RuntimeException ex) {
            throw invalid();
        }
    }

    public static void version(Long expected, Long actual) {
        if (expected == null || !expected.equals(actual))
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        if (actual == Long.MAX_VALUE) throw BusinessException.error(ErrorCode.VERSION_EXHAUSTED);
    }

    public static void contains(String prefix, String key) {
        key(key, false);
        if (!key.startsWith(prefix)) throw BusinessException.error(ErrorCode.FORBIDDEN);
    }
}
