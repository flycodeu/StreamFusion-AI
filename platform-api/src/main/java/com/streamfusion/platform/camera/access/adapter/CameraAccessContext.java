package com.streamfusion.platform.camera.access.adapter;

import java.net.URI;

/** Transient, already-authorized source snapshot; never persist or expose this object. */
public record CameraAccessContext(
        URI endpoint,
        String networkPolicyKey,
        String username,
        String password,
        int rtspPort,
        Runnable checkAuthorization,
        long deadlineNanos,
        int pageNumber,
        int pageSize) {
    public CameraAccessContext(
            URI endpoint,
            String networkPolicyKey,
            String username,
            String password,
            int rtspPort,
            Runnable checkAuthorization,
            long deadlineNanos) {
        this(
                endpoint,
                networkPolicyKey,
                username,
                password,
                rtspPort,
                checkAuthorization,
                deadlineNanos,
                1,
                100);
    }

    public CameraAccessContext(
            URI endpoint,
            String networkPolicyKey,
            String username,
            String password,
            int rtspPort,
            Runnable checkAuthorization) {
        this(
                endpoint,
                networkPolicyKey,
                username,
                password,
                rtspPort,
                java.util.Objects.requireNonNull(checkAuthorization),
                System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(60));
    }

    // HIK_PLATFORM uses username=appKey and password=appSecret, not a camera login.
    @Override
    public String toString() {
        return "CameraAccessContext[redacted]";
    }
}
