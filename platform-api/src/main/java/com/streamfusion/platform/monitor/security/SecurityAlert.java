package com.streamfusion.platform.monitor.security;

import com.streamfusion.platform.system.auth.guard.IpAddresses;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/**
 * Only bounded evidence: never raw paths, request bodies, credentials or submitted account names.
 */
public record SecurityAlert(
        String id,
        Rule rule,
        String sourceIp,
        Instant firstSeen,
        Instant lastSeen,
        int windowSeconds,
        int eventCount,
        int distinctPaths,
        Map<Category, Integer> categories,
        List<String> actorIds,
        List<String> traceIds) {
    public enum Category {
        LOGIN_FAILURE,
        ACCESS_DENIED,
        PATH_PROBE,
        CSRF_FAILURE
    }

    public enum Rule {
        LOGIN_FAILURE_BURST,
        ACCESS_DENIAL_BURST,
        PATH_SCAN,
        CSRF_FAILURE_BURST,
        MIXED_PROBING
    }

    public SecurityAlert {
        UUID.fromString(id);
        sourceIp = IpAddresses.canonical(sourceIp);
        if (rule == null
                || firstSeen == null
                || lastSeen == null
                || lastSeen.isBefore(firstSeen)
                || windowSeconds < 60
                || windowSeconds > 1800
                || eventCount < 1
                || eventCount > 128
                || distinctPaths < 1
                || distinctPaths > eventCount
                || categories == null
                || categories.isEmpty()
                || categories.size() > 4
                || categories.values().stream().anyMatch(v -> v == null || v < 1 || v > 128)
                || categories.values().stream().mapToInt(Integer::intValue).sum() != eventCount
                || actorIds == null
                || actorIds.size() > 8
                || traceIds == null
                || traceIds.size() > 5
                || actorIds.stream().anyMatch(v -> v == null || !v.matches("[1-9][0-9]{0,18}"))
                || traceIds.stream().anyMatch(v -> v == null || !v.matches("[a-f0-9]{32}")))
            throw new IllegalArgumentException("Invalid security alert evidence");
        categories = Map.copyOf(categories);
        actorIds = List.copyOf(actorIds);
        traceIds = List.copyOf(traceIds);
    }
}
