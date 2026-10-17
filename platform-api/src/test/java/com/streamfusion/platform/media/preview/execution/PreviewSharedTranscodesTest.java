package com.streamfusion.platform.media.preview.execution;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Stream;
import com.streamfusion.platform.media.preview.media.PreviewMediaGateway.Transcode;
import com.streamfusion.platform.media.preview.media.PreviewPullRegistry;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.net.URI;
import java.nio.file.Path;
import java.time.Duration;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class PreviewSharedTranscodesTest {
    final PreviewMediaGateway media = mock(PreviewMediaGateway.class);
    final PreviewPullRegistry pulls = mock(PreviewPullRegistry.class);
    final PreviewSharedTranscodes shared = new PreviewSharedTranscodes(media, pulls);
    final Stream upstream =
            new Stream(
                    new ActiveZlm(
                            URI.create("http://127.0.0.1:8088"),
                            "test",
                            Path.of("/runtime/MediaServer"),
                            Path.of("/runtime/config.ini"),
                            "1"),
                    "sf_preview_0123456789ab",
                    "0123456789abcdef0123456789abcdef",
                    "upstream-key");

    @BeforeEach
    void setup() {
        when(media.transcodeConfiguration(any(), any())).thenReturn("encoding-1");
        when(media.openViewer(any(), anyString(), anyString(), any()))
                .thenAnswer(
                        call -> {
                            String app = call.getArgument(1), id = call.getArgument(2);
                            return new Stream(
                                    upstream.runtime(),
                                    app,
                                    id,
                                    "__defaultVhost__/" + app + "/" + id);
                        });
        when(media.transcode(any(), anyString(), anyString(), any()))
                .thenAnswer(call -> new Transcode(call.getArgument(1), "owned-key"));
    }

    @Test
    void sixtyFourViewersShareOneProcessAndOnlyLastExitReleasesIt() {
        var output = shared.acquire("0", upstream, deadline());
        for (int index = 1; index < 64; index++)
            assertThat(shared.acquire(Integer.toString(index), upstream, deadline()))
                    .isEqualTo(output);
        verify(media).transcode(any(), anyString(), eq("encoding-1"), any());
        for (int index = 0; index < 63; index++)
            shared.release(Integer.toString(index), deadline());
        verify(media, never()).close(any(), any(), any());
        shared.release("63", deadline());
        verify(media).close(any(), eq(new Transcode(output.id(), "owned-key")), any());
        verify(pulls).exited(any());
    }

    @Test
    void differentEncodingSettingsAndSourcesNeverShare() {
        var first = shared.acquire("first", upstream, deadline());
        when(media.transcodeConfiguration(any(), any())).thenReturn("encoding-2");
        var second = shared.acquire("second", upstream, deadline());
        var other = new Stream(upstream.runtime(), upstream.app(), "other-source", "other-key");
        var third = shared.acquire("third", other, deadline());
        assertThat(first).isNotEqualTo(second).isNotEqualTo(third);
        verify(media, times(3)).transcode(any(), anyString(), anyString(), any());
        for (String member : new String[] {"first", "second", "third"})
            shared.release(member, deadline());
    }

    @Test
    void ownershipIsRecordedBeforeOpeningAndRemovedAfterConfirmedExit() {
        shared.acquire("viewer", upstream, deadline());
        shared.release("viewer", deadline());
        var order = inOrder(media, pulls);
        order.verify(pulls).register(any(), eq(true));
        order.verify(media).openViewer(any(), anyString(), anyString(), any());
        order.verify(pulls).started(any());
        order.verify(media).transcode(any(), anyString(), anyString(), any());
        order.verify(media).close(any(), any(), any());
        order.verify(pulls).exited(any());
    }

    @Test
    void cleanupFailureKeepsTheLastMemberAndBlocksReuseUntilConfirmedExit() {
        var first = shared.acquire("first", upstream, deadline());
        doThrow(new PreviewFailure("CLEANUP_PENDING")).when(media).close(any(), any(), any());
        assertThatThrownBy(() -> shared.release("first", deadline())).hasMessage("CLEANUP_PENDING");
        assertThatThrownBy(() -> shared.acquire("second", upstream, deadline()))
                .hasMessage("PREVIEW_PENDING");
        verify(pulls, never()).exited(any());
        doNothing().when(media).close(any(), any(), any());
        shared.release("first", deadline());
        assertThat(shared.acquire("second", upstream, deadline())).isNotEqualTo(first);
        shared.release("second", deadline());
    }

    @Test
    void unknownStartOutcomeRetainsTheExactTranscoderForCleanup() {
        var pending = new Transcode("pending-output", "pending-key");
        when(media.transcode(any(), anyString(), anyString(), any()))
                .thenThrow(new PreviewFailure("PREVIEW_TIMEOUT", pending));
        assertThatThrownBy(() -> shared.acquire("viewer", upstream, deadline()))
                .hasMessage("PREVIEW_TIMEOUT");
        shared.release("viewer", deadline());
        verify(media).close(any(), same(pending), any());
    }

    @Test
    void concurrentViewersWaitForOneCreator() throws Exception {
        var starting = new CountDownLatch(1);
        var proceed = new CountDownLatch(1);
        when(media.transcode(any(), anyString(), anyString(), any()))
                .thenAnswer(
                        call -> {
                            starting.countDown();
                            assertThat(proceed.await(3, TimeUnit.SECONDS)).isTrue();
                            return new Transcode(call.getArgument(1), "key");
                        });
        var first =
                CompletableFuture.supplyAsync(() -> shared.acquire("first", upstream, deadline()));
        assertThat(starting.await(2, TimeUnit.SECONDS)).isTrue();
        var second =
                CompletableFuture.supplyAsync(() -> shared.acquire("second", upstream, deadline()));
        try {
            verify(media, timeout(1000).times(2)).transcodeConfiguration(any(), any());
            assertThat(second).isNotDone();
        } finally {
            proceed.countDown();
        }
        assertThat(first.get(2, TimeUnit.SECONDS)).isEqualTo(second.get(2, TimeUnit.SECONDS));
        verify(media).transcode(any(), anyString(), anyString(), any());
        shared.release("first", deadline());
        shared.release("second", deadline());
    }

    @Test
    void cancelledJoinerDoesNotStopTheOtherViewer() {
        shared.acquire("first", upstream, deadline());
        var cancelled = deadline();
        cancelled.cancel();
        assertThatThrownBy(() -> shared.acquire("second", upstream, cancelled))
                .hasMessage("CANCELLED");
        shared.release("second", deadline());
        verify(media, never()).close(any(), any(), any());
        shared.release("first", deadline());
    }

    private PreviewDeadline deadline() {
        return PreviewDeadline.after(Duration.ofSeconds(5));
    }
}
