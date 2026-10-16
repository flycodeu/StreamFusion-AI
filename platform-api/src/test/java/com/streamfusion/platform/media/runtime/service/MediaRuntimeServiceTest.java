package com.streamfusion.platform.media.runtime.service;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.config.PlatformProperties;
import com.streamfusion.platform.media.runtime.config.MediaRuntimeProperties;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Candidate;
import com.streamfusion.platform.media.runtime.model.MediaRuntimeSnapshot.Selection;
import java.net.URI;
import java.net.http.HttpClient;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.boot.web.servlet.context.ServletWebServerApplicationContext;
import org.springframework.test.util.ReflectionTestUtils;

class MediaRuntimeServiceTest {
    @TempDir Path directory;
    private static final Selection NONE = new Selection(null, null);
    private static final Candidate ZLM = candidate("ZLM", "1", "READY", "/media/MediaServer");
    private static final Candidate FFMPEG = candidate("FFMPEG", "2", "READY", "/media/ffmpeg");

    @Test
    void selectsUniqueReadyVersionsWithoutRequiringAnIdleFfmpegProcess() {
        var selected = match(NONE, List.of(ZLM, FFMPEG), Map.of());
        assertThat(selected).isEqualTo(new Selection(ZLM.id(), FFMPEG.id()));
        assertThat(FFMPEG.processRunning()).isFalse();
    }

    @Test
    void preservesExistingChoicesEvenWhenTheyAreTemporarilyUnavailable() {
        var saved = new Selection(ZLM.id(), FFMPEG.id());
        var unavailable = candidate("FFMPEG", "2", "UNAVAILABLE", FFMPEG.executablePath());
        assertThat(
                        match(
                                saved,
                                List.of(
                                        ZLM,
                                        unavailable,
                                        candidate("FFMPEG", "3", "READY", "/other/ffmpeg")),
                                Map.of()))
                .isEqualTo(saved);
    }

    @Test
    void replacesMissingIdsOnlyWhenReadyCandidatesAreUnambiguous() {
        var missing = new Selection("zlm-" + "8".repeat(24), "ffmpeg-" + "8".repeat(24));
        assertThat(match(missing, List.of(ZLM, FFMPEG), Map.of()))
                .isEqualTo(new Selection(ZLM.id(), FFMPEG.id()));
        assertThat(match(missing, List.of(), Map.of())).isEqualTo(missing);
    }

    @Test
    void neverChoosesAnUnconfirmedZlmApiOrFailedFfmpeg() {
        assertThat(
                        match(
                                NONE,
                                List.of(
                                        candidate(
                                                "ZLM", "1", "API_AVAILABLE", "/media/MediaServer"),
                                        candidate("FFMPEG", "2", "UNAVAILABLE", "/media/ffmpeg")),
                                Map.of()))
                .isEqualTo(NONE);
    }

    @Test
    void leavesMultipleReadyVersionsForManualSelection() {
        assertThat(
                        match(
                                NONE,
                                List.of(
                                        ZLM,
                                        candidate("ZLM", "4", "READY", "/other/MediaServer"),
                                        FFMPEG,
                                        candidate("FFMPEG", "3", "READY", "/other/ffmpeg")),
                                Map.of()))
                .isEqualTo(NONE);
    }

    @Test
    void prefersTheFfmpegPathConfiguredByTheSelectedZlm() {
        var other = candidate("FFMPEG", "3", "READY", directory.resolve("other/ffmpeg").toString());
        var config =
                new RuntimeProbe.ZlmConfig(
                        URI.create("http://127.0.0.1:8088"),
                        "not-logged",
                        List.of(),
                        other.executablePath());
        assertThat(match(NONE, List.of(ZLM, FFMPEG, other), Map.of(ZLM.id(), config)))
                .isEqualTo(new Selection(ZLM.id(), other.id()));
        assertThat(
                        match(
                                        new Selection(ZLM.id(), FFMPEG.id()),
                                        List.of(ZLM, FFMPEG, other),
                                        Map.of(ZLM.id(), config))
                                .ffmpegId())
                .isEqualTo(FFMPEG.id());
    }

    @Test
    void persistsStartupMatchingOnceAndKeepsTheOtherOsSelection() throws Exception {
        var mapper = new ObjectMapper();
        var store = new RuntimeSelectionStore(directory, mapper);
        store.update("LINUX", new Selection(ZLM.id(), null), "0");
        var service = service(true);
        var event = mock(ApplicationReadyEvent.class);
        when(event.getApplicationContext())
                .thenReturn(mock(ServletWebServerApplicationContext.class));
        seed(service, List.of(ZLM, FFMPEG));
        service.onReady(event);
        assertThat(store.load().selection("WINDOWS"))
                .isEqualTo(new Selection(ZLM.id(), FFMPEG.id()));
        assertThat(store.load().selection("LINUX")).isEqualTo(new Selection(ZLM.id(), null));
        assertThat(store.load().revision()).isEqualTo(2);
        service.onReady(event);
        assertThat(store.load().revision()).isEqualTo(2);
        assertThat(service.activeFfmpeg()).isPresent();
    }

    @Test
    void skipsBootstrapAndDisabledStartupMatching() throws Exception {
        var event = mock(ApplicationReadyEvent.class);
        service(true).onReady(event);
        when(event.getApplicationContext())
                .thenReturn(mock(ServletWebServerApplicationContext.class));
        service(false).onReady(event);
        assertThat(directory.resolve("media-runtime-selection.json")).doesNotExist();
    }

    @Test
    void corruptSelectionDoesNotPreventApplicationReadyOrOverwriteTheFile() throws Exception {
        var file = directory.resolve("media-runtime-selection.json");
        Files.writeString(file, "broken");
        var service = service(true);
        seed(service, List.of(ZLM, FFMPEG));
        var event = mock(ApplicationReadyEvent.class);
        when(event.getApplicationContext())
                .thenReturn(mock(ServletWebServerApplicationContext.class));
        service.onReady(event);
        assertThat(Files.readString(file)).isEqualTo("broken");
    }

    @Test
    void previewLookupsNeverStartDiscoveryEvenBeforeTheInitialInventory() {
        var service = org.mockito.Mockito.spy(service(false));
        assertThat(service.activeZlm()).isEmpty();
        assertThat(service.activeFfmpeg()).isEmpty();
        org.mockito.Mockito.verify(service, org.mockito.Mockito.never()).detect();
        org.mockito.Mockito.verify(service, org.mockito.Mockito.never()).current();
    }

    @Test
    void recoveryMatchesOnlyTrustedDiscoveredRuntimeWithoutChangingSelection() throws Exception {
        var service = org.mockito.Mockito.spy(service(false));
        String configPath =
                directory.resolve("old/config.ini").toAbsolutePath().normalize().toString();
        var candidate =
                new Candidate(
                        ZLM.id(),
                        "ZLM",
                        ZLM.executablePath(),
                        configPath,
                        "http://127.0.0.1:8088",
                        "test",
                        "READY",
                        true,
                        List.of(7L),
                        List.of(),
                        Instant.now(),
                        null);
        var config =
                new RuntimeProbe.ZlmConfig(
                        URI.create("http://127.0.0.1:8088"), "not-persisted", List.of(), null);
        seed(service, List.of(candidate), System.nanoTime(), Map.of(candidate.id(), config));
        assertThat(service.activeZlm()).isEmpty();
        var recovered = service.recoveryZlm("http://127.0.0.1:8088", configPath);
        assertThat(recovered).isPresent();
        assertThat(recovered.orElseThrow().configPath().toString()).isEqualTo(configPath);
        assertThat(service.recoveryZlm("http://10.0.0.1:8088", configPath)).isEmpty();
        assertThat(
                        service.recoveryZlm(
                                "http://127.0.0.1:8088",
                                directory.resolve("unknown.ini").toString()))
                .isEmpty();
        assertThat(service.activeZlm()).isEmpty();
        org.mockito.Mockito.verify(service, org.mockito.Mockito.never()).detect();
        org.mockito.Mockito.verify(service, org.mockito.Mockito.never()).current();
    }

    @Test
    void previewUsesTheDiscoveredSelectionWithoutAnExpiredCacheRescan() throws Exception {
        new RuntimeSelectionStore(directory, new ObjectMapper())
                .update("WINDOWS", new Selection(null, FFMPEG.id()), "0");
        var service = org.mockito.Mockito.spy(service(false));
        seed(service, List.of(FFMPEG), System.nanoTime() - Duration.ofMinutes(1).toNanos());
        assertThat(service.activeFfmpeg()).isPresent();
        org.mockito.Mockito.verify(service, org.mockito.Mockito.never()).detect();
        org.mockito.Mockito.verify(service, org.mockito.Mockito.never()).current();
    }

    private MediaRuntimeService service(boolean automatic) {
        var properties =
                new MediaRuntimeProperties(
                        automatic,
                        Duration.ofSeconds(15),
                        Duration.ofMillis(500),
                        Duration.ofSeconds(2),
                        0,
                        100,
                        4,
                        null,
                        null);
        return new MediaRuntimeService(
                properties,
                new PlatformProperties(directory, Duration.ofSeconds(15)),
                new ObjectMapper(),
                "Windows 11",
                HttpClient.newHttpClient());
    }

    private static void seed(MediaRuntimeService service, List<Candidate> candidates)
            throws Exception {
        seed(service, candidates, System.nanoTime());
    }

    private static void seed(
            MediaRuntimeService service, List<Candidate> candidates, long detectedAt)
            throws Exception {
        seed(service, candidates, detectedAt, Map.of());
    }

    private static void seed(
            MediaRuntimeService service,
            List<Candidate> candidates,
            long detectedAt,
            Map<String, RuntimeProbe.ZlmConfig> configs)
            throws Exception {
        var type = Class.forName(MediaRuntimeService.class.getName() + "$Inventory");
        var constructor = type.getDeclaredConstructors()[0];
        constructor.setAccessible(true);
        ReflectionTestUtils.setField(
                service,
                "inventory",
                constructor.newInstance(candidates, configs, Instant.now(), detectedAt, List.of()));
    }

    private static Selection match(
            Selection saved,
            List<Candidate> candidates,
            Map<String, RuntimeProbe.ZlmConfig> configs) {
        return MediaRuntimeService.matchSelection(saved, candidates, configs);
    }

    private static Candidate candidate(String kind, String digit, String status, String path) {
        return new Candidate(
                kind.toLowerCase() + "-" + digit.repeat(24),
                kind,
                path,
                null,
                null,
                "test",
                status,
                kind.equals("ZLM"),
                kind.equals("ZLM") ? List.of(7L) : List.of(),
                List.of(),
                Instant.now(),
                null);
    }
}
