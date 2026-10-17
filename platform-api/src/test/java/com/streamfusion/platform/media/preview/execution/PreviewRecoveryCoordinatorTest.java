package com.streamfusion.platform.media.preview.execution;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewPullRegistry;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService;
import java.net.URI;
import java.nio.file.Path;
import java.util.List;
import java.util.Optional;
import java.util.stream.IntStream;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class PreviewRecoveryCoordinatorTest {
    final PreviewPullRegistry pulls = mock(PreviewPullRegistry.class);
    final PreviewMediaGateway media = mock(PreviewMediaGateway.class);
    final MediaRuntimeService runtimes = mock(MediaRuntimeService.class);
    final PreviewRecoveryCoordinator recovery =
            new PreviewRecoveryCoordinator(pulls, media, runtimes);
    final MediaRuntimeService.ActiveZlm runtime =
            new MediaRuntimeService.ActiveZlm(
                    URI.create("http://127.0.0.1:8088"),
                    "secret",
                    Path.of("MediaServer.exe"),
                    Path.of("config.ini"),
                    "1");

    @BeforeEach
    void setup() {
        when(runtimes.activeZlm()).thenReturn(Optional.of(runtime));
    }

    @Test
    void stoppingAndDeferredEntriesNeverCloseResources() {
        when(pulls.pending()).thenReturn(List.of(entry(1, 0)));
        recovery.reconcile(() -> true);
        when(pulls.pending()).thenReturn(List.of(entry(1, Long.MAX_VALUE)));
        recovery.reconcile(() -> false);
        verifyNoInteractions(media);
        verify(pulls, never()).exited(any());
    }

    @Test
    void failuresKeepResponsibilityAndStopAfterSixAttempts() {
        when(pulls.pending()).thenReturn(List.of(entry(1, 0)));
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).closeRecovered(any(), any());
        for (int i = 0; i < 8; i++) recovery.reconcile(() -> false);
        verify(media, times(6)).closeRecovered(any(), any());
        verify(pulls, never()).exited(any());
    }

    @Test
    void oneSweepIsBoundedAndClosesOnlyPersistedExactTuples() {
        when(pulls.pending())
                .thenReturn(IntStream.range(0, 17).mapToObj(i -> entry(i, 0)).toList());
        recovery.reconcile(() -> false);
        var streams = org.mockito.ArgumentCaptor.forClass(PreviewMediaGateway.Stream.class);
        verify(media, times(16)).closeRecovered(streams.capture(), any());
        assertThat(streams.getAllValues())
                .allSatisfy(
                        stream -> {
                            assertThat(stream.runtime()).isSameAs(runtime);
                            assertThat(stream.app()).isEqualTo("sf_preview_abcdef012345");
                            assertThat(stream.proxyKey())
                                    .isEqualTo(
                                            "__defaultVhost__/" + stream.app() + "/" + stream.id());
                        });
        verify(pulls, times(16)).exited(any());
    }

    private PreviewPullRegistry.Entry entry(int id, long notBefore) {
        return new PreviewPullRegistry.Entry(
                runtime.apiBase().toString(),
                runtime.configPath().toAbsolutePath().normalize().toString(),
                "sf_preview_abcdef012345",
                String.format("%032x", id),
                true,
                notBefore);
    }
}
