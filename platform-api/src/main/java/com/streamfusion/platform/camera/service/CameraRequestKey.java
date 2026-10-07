package com.streamfusion.platform.camera.service;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.time.Duration;
import java.time.Instant;

/** Shared bounded-lifetime request key for asset receipts and discovery/import tickets. */
public final class CameraRequestKey {
    private CameraRequestKey() {}

    public static Instant timestamp(String value) {
        if (value == null
                || !value.matches(
                        "[0-9]{13}-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}"))
            throw CameraSourceRules.invalid();
        return Instant.ofEpochMilli(Long.parseLong(value.substring(0, 13)));
    }

    public static void requireFresh(Instant requested, Instant now) {
        if (Math.abs(Duration.between(requested, now).toMillis()) > 300_000)
            throw BusinessException.error(ErrorCode.CONFLICT);
    }
}
