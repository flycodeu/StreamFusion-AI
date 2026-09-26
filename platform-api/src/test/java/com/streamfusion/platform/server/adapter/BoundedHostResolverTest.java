package com.streamfusion.platform.server.adapter;

import static org.assertj.core.api.Assertions.assertThat;

import java.net.InetAddress;
import java.time.Duration;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

class BoundedHostResolverTest {
    @Test
    void hungNativeLookupCannotBlockLiteralProbesOrAccumulateLookups() throws Exception {
        var entered = new CountDownLatch(1);
        var release = new CountDownLatch(1);
        var calls = new AtomicInteger();
        var resolver =
                new BoundedHostResolver(
                        host -> {
                            calls.incrementAndGet();
                            entered.countDown();
                            while (release.getCount() > 0) {
                                try {
                                    release.await(2, TimeUnit.SECONDS);
                                } catch (InterruptedException ignored) {
                                    /* Model native DNS ignoring interruption. */
                                }
                            }
                            return InetAddress.getLoopbackAddress();
                        });
        try {
            assertThat(resolver.resolve("first.example", Duration.ofMillis(20))).isNull();
            assertThat(entered.await(1, TimeUnit.SECONDS)).isTrue();
            for (int i = 0; i < 10; i++)
                assertThat(resolver.resolve("other.example", Duration.ofMillis(20))).isNull();
            assertThat(resolver.resolve("first.example", Duration.ofMillis(20))).isNull();
            assertThat(resolver.resolve("127.0.0.1", Duration.ofMillis(20)).isLoopbackAddress())
                    .isTrue();
            assertThat(resolver.resolve("::1", Duration.ofMillis(20)).isLoopbackAddress()).isTrue();
            assertThat(calls).hasValue(1);
        } finally {
            release.countDown();
            resolver.close();
        }
    }

    @Test
    void completedLookupsHandOffToNextEndpointWithoutRejection() {
        var calls = new AtomicInteger();
        var resolver =
                new BoundedHostResolver(
                        host -> {
                            calls.incrementAndGet();
                            return InetAddress.getLoopbackAddress();
                        });
        try {
            for (int i = 0; i < 50; i++)
                assertThat(resolver.resolve("host" + i + ".example", Duration.ofSeconds(1)))
                        .isNotNull();
            assertThat(calls).hasValue(50);
        } finally {
            resolver.close();
        }
    }
}
