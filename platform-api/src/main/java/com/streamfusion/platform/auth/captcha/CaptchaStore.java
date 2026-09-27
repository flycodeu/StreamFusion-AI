package com.streamfusion.platform.auth.captcha;

import com.streamfusion.platform.auth.guard.IpGuardProperties;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.util.Base64;
import java.util.HexFormat;
import java.util.List;
import lombok.RequiredArgsConstructor;
import org.springframework.dao.DataAccessResourceFailureException;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.data.redis.core.script.DefaultRedisScript;
import org.springframework.stereotype.Component;

/**
 * One expiring slot per anonymous session; Lua serializes refresh, image lookup and consumption.
 */
@Component
@RequiredArgsConstructor
public class CaptchaStore {
    public static final Duration LIFETIME = Duration.ofMinutes(2);
    private static final DefaultRedisScript<Long> REPLACE =
            new DefaultRedisScript<>(
                    """
            redis.call('DEL',KEYS[1])
            redis.call('HSET',KEYS[1],'id',ARGV[1],'answer',ARGV[2],'image',ARGV[3])
            redis.call('PEXPIRE',KEYS[1],ARGV[4])
            return 1
            """,
                    Long.class);
    private static final DefaultRedisScript<String> IMAGE =
            new DefaultRedisScript<>(
                    """
            if redis.call('HGET',KEYS[1],'id') ~= ARGV[1] then return nil end
            return redis.call('HGET',KEYS[1],'image')
            """,
                    String.class);
    private static final DefaultRedisScript<Long> CONSUME =
            new DefaultRedisScript<>(
                    """
            if redis.call('HGET',KEYS[1],'id') ~= ARGV[1] then return 0 end
            local answer=redis.call('HGET',KEYS[1],'answer')
            redis.call('DEL',KEYS[1])
            if answer == ARGV[2] then return 1 end
            return 0
            """,
                    Long.class);
    private final StringRedisTemplate redis;
    private final IpGuardProperties properties;

    public void replace(String sessionId, String captchaId, String answerHash, byte[] image) {
        Long saved =
                redis.execute(
                        REPLACE,
                        List.of(key(sessionId)),
                        captchaId,
                        answerHash,
                        Base64.getEncoder().encodeToString(image),
                        Long.toString(LIFETIME.toMillis()));
        if (!Long.valueOf(1).equals(saved))
            throw new DataAccessResourceFailureException("Captcha store unavailable");
    }

    public byte[] image(String sessionId, String captchaId) {
        String encoded = redis.execute(IMAGE, List.of(key(sessionId)), captchaId);
        return encoded == null ? null : Base64.getDecoder().decode(encoded);
    }

    public boolean consume(String sessionId, String captchaId, String answerHash) {
        Long accepted = redis.execute(CONSUME, List.of(key(sessionId)), captchaId, answerHash);
        if (accepted == null)
            throw new DataAccessResourceFailureException("Captcha store unavailable");
        return accepted == 1;
    }

    public static String digest(String value) {
        try {
            return HexFormat.of()
                    .formatHex(
                            MessageDigest.getInstance("SHA-256")
                                    .digest(value.getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException impossible) {
            throw new IllegalStateException(impossible);
        }
    }

    private String key(String sessionId) {
        return properties.redisNamespace() + ":captcha:" + digest(sessionId);
    }
}
