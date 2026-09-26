package com.streamfusion.platform.auth.guard;

import static org.assertj.core.api.Assertions.assertThat;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.Callable;
import java.util.concurrent.Executors;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.springframework.data.redis.connection.lettuce.LettuceConnectionFactory;
import org.springframework.data.redis.core.StringRedisTemplate;

/** Real atomic script evidence against the explicitly started isolated, nonpersistent Redis. */
@EnabledIfSystemProperty(named = "sf.test.ipGuardRedis", matches = "true")
class LoginGuardRedisTest {
    private LettuceConnectionFactory factory;
    private StringRedisTemplate redis;
    private LoginGuardStore store;
    private String namespace;

    @BeforeEach
    void connect() {
        namespace = "account-ux-security01-test:" + UUID.randomUUID();
        factory = new LettuceConnectionFactory("127.0.0.1", 16379);
        factory.afterPropertiesSet();
        factory.start();
        redis = new StringRedisTemplate(factory);
        store =
                new LoginGuardStore(
                        redis,
                        new IpGuardProperties(
                                true,
                                20,
                                Duration.ofMinutes(10),
                                120,
                                Duration.ofSeconds(1),
                                namespace,
                                List.of()));
    }

    @AfterEach
    void cleanOwnKeys() {
        if (redis != null) {
            var keys = redis.keys(namespace + ":*");
            if (keys != null && !keys.isEmpty()) redis.delete(keys);
        }
        if (factory != null) factory.destroy();
    }

    @Test
    void concurrentFailuresHaveNoLostIncrementsAndGenerationIsolatesManualRelease()
            throws Exception {
        try (var workers = Executors.newFixedThreadPool(8)) {
            List<Callable<Long>> tasks = new ArrayList<>();
            for (int i = 0; i < 100; i++) tasks.add(() -> store.failure("2001:db8::1", 0).count());
            List<Long> counts = new ArrayList<>();
            for (var future : workers.invokeAll(tasks)) counts.add(future.get());
            assertThat(counts).hasSize(100).doesNotHaveDuplicates().contains(1L, 20L, 100L);
        }
        assertThat(store.failure("2001:db8::1", 2).count()).isEqualTo(1);
        assertThat(store.failure("2001:db8::1", 0).retryAfterSeconds()).isBetween(1L, 600L);
    }

    @Test
    void challengeClaimHasExactlyOneWinnerAcrossConcurrentConsumers() throws Exception {
        try (var workers = Executors.newFixedThreadPool(8)) {
            List<Callable<Boolean>> tasks = new ArrayList<>();
            for (int i = 0; i < 32; i++)
                tasks.add(() -> store.claimChallenge("synthetic-challenge"));
            long successes = 0;
            for (var future : workers.invokeAll(tasks)) if (future.get()) successes++;
            assertThat(successes).isEqualTo(1);
            assertThat(redis.getExpire(namespace + ":challenge-used:synthetic-challenge"))
                    .isBetween(1L, 120L);
        }
    }

    @Test
    void resourceWindowExpiresWithoutCreatingPersistentBlockState() throws Exception {
        assertThat(store.resource("192.0.2.10").count()).isEqualTo(1);
        assertThat(store.resource("192.0.2.10").count()).isEqualTo(2);
        Thread.sleep(1100);
        assertThat(store.resource("192.0.2.10").count()).isEqualTo(1);
    }
}
