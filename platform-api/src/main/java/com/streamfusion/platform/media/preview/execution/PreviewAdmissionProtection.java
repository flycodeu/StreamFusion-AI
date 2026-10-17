package com.streamfusion.platform.media.preview.execution;

import java.lang.management.ManagementFactory;
import java.time.Instant;
import java.util.HashMap;
import java.util.Map;

/**
 * Additional overload protection. Reservations remain authoritative and sessions are never killed.
 */
public final class PreviewAdmissionProtection {
    public record Observation(
            Double cpuLoad, Double memoryUsed, String reasonCode, Instant observedAt) {}

    private record Counter(int high, int low, boolean blocked) {}

    private final Map<String, Counter> bandwidth = new HashMap<>();
    private Counter host = new Counter(0, 0, false);
    private Observation observation = new Observation(null, null, null, null);

    public synchronized void sampleHost() {
        var bean = ManagementFactory.getOperatingSystemMXBean();
        if (bean instanceof com.sun.management.OperatingSystemMXBean os) {
            double cpu = os.getCpuLoad();
            long total = os.getTotalMemorySize(), free = os.getFreeMemorySize();
            sampleHost(
                    cpu < 0 ? null : cpu,
                    total <= 0 ? null : 1.0 - (double) free / total,
                    Instant.now());
        }
    }

    synchronized void sampleHost(Double cpu, Double memory, Instant now) {
        boolean high = cpu != null && cpu >= .95 || memory != null && memory >= .95;
        boolean low = cpu != null && memory != null && cpu <= .85 && memory <= .85;
        host = advance(host, high, low);
        observation = new Observation(cpu, memory, reason(), now);
    }

    public synchronized void sampleBandwidth(String key, Double kbps, long upper) {
        if (kbps == null || !Double.isFinite(kbps) || kbps < 0) return;
        var old = bandwidth.getOrDefault(key, new Counter(0, 0, false));
        bandwidth.put(key, advance(old, kbps > upper, kbps <= upper * .85));
    }

    public synchronized void retain(java.util.Set<String> keys) {
        bandwidth.keySet().retainAll(keys);
    }

    public synchronized String reason() {
        return host.blocked()
                ? "NODE_HOST_OVERLOADED"
                : bandwidth.values().stream().anyMatch(Counter::blocked)
                        ? "NODE_BITRATE_EXCEEDED"
                        : null;
    }

    public synchronized Observation observation() {
        return new Observation(
                observation.cpuLoad(),
                observation.memoryUsed(),
                reason(),
                observation.observedAt());
    }

    private Counter advance(Counter old, boolean high, boolean low) {
        int h = high ? Math.min(3, old.high() + 1) : 0, l = low ? Math.min(3, old.low() + 1) : 0;
        return new Counter(h, l, h >= 3 || old.blocked() && l < 3);
    }
}
