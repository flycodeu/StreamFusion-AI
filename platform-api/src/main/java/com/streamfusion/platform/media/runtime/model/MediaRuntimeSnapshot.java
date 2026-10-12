package com.streamfusion.platform.media.runtime.model;

import java.time.Instant;
import java.util.List;

public record MediaRuntimeSnapshot(
        String os,
        String version,
        Instant checkedAt,
        Selection selected,
        List<Candidate> candidates,
        List<Diagnostic> scanDiagnostics) {
    public record Selection(String zlmId, String ffmpegId) {}

    public record Candidate(
            String id,
            String kind,
            String executablePath,
            String configPath,
            String apiBase,
            String softwareVersion,
            String status,
            boolean processRunning,
            List<Long> pids,
            List<Port> ports,
            Instant checkedAt,
            Diagnostic diagnostic) {}

    /** Configured ports are kept separate from a successful TCP reachability observation. */
    public record Port(String protocol, int port, Boolean reachable) {}

    public record Diagnostic(String code, String message) {}
}
