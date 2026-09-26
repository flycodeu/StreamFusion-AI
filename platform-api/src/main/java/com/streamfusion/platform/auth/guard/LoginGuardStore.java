package com.streamfusion.platform.auth.guard;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.util.HexFormat;
import java.util.List;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.stereotype.Component;

@Component
public class LoginGuardStore {
    private static final DefaultRedisScript<List> INCREMENT =
            new DefaultRedisScript<>(
                    "local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]); end; return {n,redis.call('PTTL',KEYS[1])}",
                    List.class);
    private final StringRedisTemplate redis;
    private final IpGuardProperties properties;

    public LoginGuardStore(StringRedisTemplate redis, IpGuardProperties properties) {
        this.redis = redis;
        this.properties = properties;
    }

    public Counter failure(String ip, long generation) {
        return increment("failure:" + key(ip) + ":" + generation, properties.failureWindow());
    }

    public Counter resource(String ip) {
        return increment("resource:" + key(ip), properties.resourceWindow());
    }

    public boolean claimChallenge(String challengeId) {
        Boolean claimed =
                redis.opsForValue()
                        .setIfAbsent(
                                properties.redisNamespace() + ":challenge-used:" + challengeId,
                                "1",
                                Duration.ofSeconds(120));
        if (claimed == null)
            throw new org.springframework.dao.DataAccessResourceFailureException(
                    "Challenge guard unavailable");
        return claimed;
    }

    private Counter increment(String suffix, Duration duration) {
        List<?> result =
                redis.execute(
                        INCREMENT,
                        List.of(properties.redisNamespace() + ":" + suffix),
                        Long.toString(duration.toMillis()));
        if (result == null
                || result.size() != 2
                || !(result.get(0) instanceof Number count)
                || !(result.get(1) instanceof Number ttl)
                || ttl.longValue() < 0)
            throw new org.springframework.dao.DataAccessResourceFailureException(
                    "IP guard state unavailable");
        return new Counter(count.longValue(), Math.max(1, (ttl.longValue() + 999) / 1000));
    }

    private static String key(String ip) {
        try {
            return HexFormat.of()
                    .formatHex(
                            MessageDigest.getInstance("SHA-256")
                                    .digest(ip.getBytes(StandardCharsets.US_ASCII)));
        } catch (NoSuchAlgorithmException impossible) {
            throw new IllegalStateException(impossible);
        }
    }

    public record Counter(long count, long retryAfterSeconds) {}
}
