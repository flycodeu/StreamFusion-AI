package com.streamfusion.platform.inspection.evidence;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.verifyNoInteractions;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;

import com.streamfusion.platform.camera.status.CameraStatusWorker;
import com.streamfusion.platform.support.CameraTestSupport;
import com.zaxxer.hikari.HikariDataSource;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.Callable;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Test;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/** Opt-in same-host interference probe. H2 results are not a production capacity claim. */
class InspectionOnlineLoadIntegrationTest extends CameraTestSupport {
    @MockitoBean private CameraStatusWorker worker;

    @Test
    void onlineStatusRemainsAvailableWhileIsolatedStorageMatrixRuns() throws Exception {
        String signal = System.getenv("SF_INSPECTION_ONLINE_LOAD_SIGNAL");
        String output = System.getenv("SF_INSPECTION_ONLINE_LOAD_RESULT");
        Assumptions.assumeTrue(signal != null && output != null, "Run via verify-online-load.ps1");
        Path signalPath = Path.of(signal).toAbsolutePath().normalize();
        Path resultPath = Path.of(output).toAbsolutePath().normalize();
        assertThat(signalPath.getParent()).isEqualTo(resultPath.getParent());
        Files.createDirectories(resultPath.getParent());
        Path donePath = signalPath.resolveSibling("storage-done.signal");

        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        for (int i = 0; i < 20; i++) camera(auth, sourceId, "inspection-load-" + i);
        assertThat(probe(auth)).isEqualTo(200);
        var hikari = dataSource.unwrap(HikariDataSource.class).getHikariPoolMXBean();

        measure(auth, hikari, 5, null); // Warm the isolated MVC/JDBC path before comparison.
        Phase baseline = measure(auth, hikari, 8, null);
        Files.writeString(signalPath, Instant.now().toString());
        Phase duringStorage = measure(auth, hikari, 120, donePath);
        Map<String, Object> evidence = new LinkedHashMap<>();
        evidence.put("fixture", "isolated H2 / Spring MockMvc / 20 synthetic camera channels");
        evidence.put("remoteSourceRead", false);
        evidence.put("poolMaximum", dataSource.unwrap(HikariDataSource.class).getMaximumPoolSize());
        evidence.put("baseline", baseline.summary());
        evidence.put("duringStorage", duringStorage.summary());
        evidence.put("comparisonScope", "same-host isolated upload interference only");
        Files.writeString(
                resultPath, json.writerWithDefaultPrettyPrinter().writeValueAsString(evidence));

        assertThat(baseline.failures()).isZero();
        assertThat(duringStorage.failures()).isZero();
        assertThat(duringStorage.count()).isGreaterThan(100);
        verifyNoInteractions(worker);
    }

    private int probe(Csrf auth) throws Exception {
        return mvc.perform(get("/camera-status?layer=CONTROL&size=20").session(auth.session()))
                .andReturn()
                .getResponse()
                .getStatus();
    }

    private Phase measure(
            Csrf auth, com.zaxxer.hikari.HikariPoolMXBean hikari, int seconds, Path stopSignal)
            throws Exception {
        Instant start = Instant.now();
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(seconds);
        List<Long> latencies = Collections.synchronizedList(new ArrayList<>());
        List<Integer> failures = Collections.synchronizedList(new ArrayList<>());
        var maxActive = new java.util.concurrent.atomic.AtomicInteger();
        var maxWaiting = new java.util.concurrent.atomic.AtomicInteger();
        try (var pool = Executors.newFixedThreadPool(3)) {
            List<Callable<Void>> workers = new ArrayList<>();
            for (int i = 0; i < 3; i++) {
                workers.add(
                        () -> {
                            while (System.nanoTime() < deadline
                                    && (stopSignal == null || !Files.exists(stopSignal))) {
                                long before = System.nanoTime();
                                try {
                                    int status = probe(auth);
                                    if (status != 200) failures.add(status);
                                } catch (Exception error) {
                                    failures.add(0);
                                }
                                latencies.add(
                                        TimeUnit.NANOSECONDS.toMicros(System.nanoTime() - before));
                                maxActive.accumulateAndGet(
                                        hikari.getActiveConnections(), Math::max);
                                maxWaiting.accumulateAndGet(
                                        hikari.getThreadsAwaitingConnection(), Math::max);
                                try {
                                    Thread.sleep(20);
                                } catch (InterruptedException interrupted) {
                                    Thread.currentThread().interrupt();
                                    return null;
                                }
                            }
                            return null;
                        });
            }
            for (var future : pool.invokeAll(workers)) future.get();
        }
        return new Phase(
                start,
                Instant.now(),
                List.copyOf(latencies),
                List.copyOf(failures),
                maxActive.get(),
                maxWaiting.get());
    }

    private record Phase(
            Instant startedAt,
            Instant endedAt,
            List<Long> latencyMicros,
            List<Integer> errorStatuses,
            int maximumActiveConnections,
            int maximumWaitingConnections) {
        int count() {
            return latencyMicros.size();
        }

        int failures() {
            return errorStatuses.size();
        }

        Map<String, Object> summary() {
            var sorted = latencyMicros.stream().sorted().toList();
            Map<String, Object> value = new LinkedHashMap<>();
            value.put("startedAt", startedAt);
            value.put("endedAt", endedAt);
            value.put("requests", count());
            value.put("failures", failures());
            value.put("errorStatuses", errorStatuses);
            value.put("p50Ms", percentile(sorted, 50) / 1000.0);
            value.put("p95Ms", percentile(sorted, 95) / 1000.0);
            value.put("p99Ms", percentile(sorted, 99) / 1000.0);
            value.put("maximumActiveConnections", maximumActiveConnections);
            value.put("maximumWaitingConnections", maximumWaitingConnections);
            return value;
        }

        private static long percentile(List<Long> sorted, int percentage) {
            return sorted.isEmpty() ? 0 : sorted.get((sorted.size() - 1) * percentage / 100);
        }
    }
}
