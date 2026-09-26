package com.streamfusion.platform.common.exception.details;

import java.time.Instant;

/** Only the affected account's login summary; no session identifier or raw user agent. */
public record SessionEndedDetails(
        String reason,
        Instant occurredAt,
        Instant loginAt,
        String sourceIp,
        String regionType,
        String region,
        String browser,
        String os) {
    public SessionEndedDetails {
        if (!"REPLACED".equals(reason) && !"FORCED_LOGOUT".equals(reason)) {
            throw new IllegalArgumentException("Unsupported session end reason");
        }
        sourceIp = bounded(sourceIp, 64);
        regionType = bounded(regionType, 32);
        region = bounded(region, 200);
        browser = bounded(browser, 64);
        os = bounded(os, 64);
    }

    private static String bounded(String value, int length) {
        return value == null ? null : value.substring(0, Math.min(value.length(), length));
    }
}
