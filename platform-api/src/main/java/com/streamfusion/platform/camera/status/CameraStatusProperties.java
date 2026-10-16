package com.streamfusion.platform.camera.status;

import lombok.Getter;
import lombok.Setter;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.stereotype.Component;

@Getter
@Setter
@Component
@ConfigurationProperties(prefix = "platform.camera.status")
public class CameraStatusProperties {
    private int workerThreads = 4;
    private int queueCapacity = 8;
    private long startIntervalMs = 250;
    private long probeTimeoutMs = 10_000;
    private long executionTimeoutMs = 30_000;
    // Legacy validUntil metadata only; the latest matching observation no longer expires.
    private long freshnessMs = 300_000;
    private int retentionDays = 7;

    public void validate() {
        if (workerThreads < 1
                || workerThreads > 8
                || queueCapacity < 1
                || queueCapacity > 32
                || startIntervalMs < 100
                || startIntervalMs > 60_000
                || probeTimeoutMs < 1000
                || probeTimeoutMs > 30_000
                || executionTimeoutMs <= probeTimeoutMs
                || executionTimeoutMs > 120_000
                || freshnessMs < 30_000
                || freshnessMs > 3_600_000
                || retentionDays < 1
                || retentionDays > 30)
            throw new IllegalArgumentException("Invalid camera status limits");
    }
}
