package com.streamfusion.platform.auth.captcha;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.auth.guard.IpGuardProperties;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.Callable;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.springframework.data.redis.connection.lettuce.LettuceConnectionFactory;
import org.springframework.data.redis.core.StringRedisTemplate;

@EnabledIfSystemProperty(named = "sf.test.captchaRedis", matches = "true")
class CaptchaStoreRedisTest {
    private LettuceConnectionFactory factory;
    private StringRedisTemplate redis;
    private CaptchaStore store;
    private String namespace;

    @BeforeEach
    void connect() {
        namespace = "captcha-test:" + UUID.randomUUID();
        factory = new LettuceConnectionFactory("127.0.0.1", 16379);
        factory.afterPropertiesSet();
        factory.start();
        redis = new StringRedisTemplate(factory);
        store =
                new CaptchaStore(
                        redis,
                        new IpGuardProperties(
                                true,
                                20,
                                Duration.ofMinutes(10),
                                120,
                                Duration.ofMinutes(1),
                                namespace,
                                List.of()));
    }

    @AfterEach
    void close() {
        if (redis != null) {
            var keys = redis.keys(namespace + ":*");
            if (keys != null && !keys.isEmpty()) redis.delete(keys);
        }
        if (factory != null) factory.destroy();
    }

    @Test
    void onlyOneConcurrentConsumerCanAcceptTheCaptcha() throws Exception {
        store.replace("session", "captcha", "hash", new byte[] {1, 2});
        try (var workers = Executors.newFixedThreadPool(8)) {
            List<Callable<Boolean>> calls = new ArrayList<>();
            for (int i = 0; i < 32; i++)
                calls.add(() -> store.consume("session", "captcha", "hash"));
            int accepted = 0;
            for (var future : workers.invokeAll(calls)) if (future.get()) accepted++;
            assertThat(accepted).isEqualTo(1);
        }
        assertThat(store.image("session", "captcha")).isNull();
    }

    @Test
    void refreshInvalidatesTheOldCodeAndLateRequestsCannotDeleteTheNewCode() throws Exception {
        store.replace("session", "old", "old-hash", new byte[] {1});
        store.replace("session", "new", "new-hash", new byte[] {2});
        assertThat(store.image("session", "old")).isNull();
        try (var workers = Executors.newFixedThreadPool(8)) {
            List<Callable<Boolean>> calls = new ArrayList<>();
            for (int i = 0; i < 16; i++)
                calls.add(() -> store.consume("session", "old", "old-hash"));
            for (var future : workers.invokeAll(calls)) assertThat(future.get()).isFalse();
        }
        assertThat(store.image("session", "new")).containsExactly(2);
        assertThat(store.consume("session", "new", "new-hash")).isTrue();
    }

    @Test
    void wrongAnswersConsumeTheirCodeButOtherSessionsCannotReadOrConsumeIt() {
        store.replace("session", "captcha", "hash", new byte[] {1});
        assertThat(store.image("other", "captcha")).isNull();
        assertThat(store.consume("other", "captcha", "hash")).isFalse();
        assertThat(store.consume("session", "captcha", "wrong")).isFalse();
        assertThat(store.consume("session", "captcha", "hash")).isFalse();
    }

    @Test
    void ttlIsTwoMinutesAndImageReadsDoNotProlongIt() throws Exception {
        store.replace("session", "captcha", "hash", new byte[] {1});
        String key = namespace + ":captcha:" + CaptchaStore.digest("session");
        assertThat(key).doesNotContain("session");
        Long before = redis.getExpire(key, TimeUnit.MILLISECONDS);
        assertThat(before).isBetween(118000L, 120000L);
        for (int i = 0; i < 3; i++)
            assertThat(store.image("session", "captcha")).containsExactly(1);
        assertThat(redis.getExpire(key, TimeUnit.MILLISECONDS)).isLessThanOrEqualTo(before);
        redis.expire(key, Duration.ofMillis(40));
        Thread.sleep(100);
        assertThat(store.image("session", "captcha")).isNull();
        assertThat(store.consume("session", "captcha", "hash")).isFalse();
    }
}
