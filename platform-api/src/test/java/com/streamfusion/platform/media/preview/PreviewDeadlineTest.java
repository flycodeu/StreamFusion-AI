package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;

import java.time.Duration;
import java.util.concurrent.atomic.AtomicLong;
import org.junit.jupiter.api.Test;

class PreviewDeadlineTest {
    @Test
    void everyStageConsumesTheSameMonotonicBudget() {
        var nanos = new AtomicLong(100);
        var deadline = PreviewDeadline.after(Duration.ofSeconds(18), nanos::get);
        nanos.addAndGet(Duration.ofSeconds(10).toNanos());
        assertThat(deadline.remaining()).isEqualTo(Duration.ofSeconds(8));
        nanos.addAndGet(Duration.ofSeconds(7).toNanos());
        assertThat(deadline.remaining()).isEqualTo(Duration.ofSeconds(1));
        nanos.addAndGet(Duration.ofSeconds(1).toNanos());
        assertThatThrownBy(deadline::check).hasMessage("PREVIEW_TIMEOUT");
    }

    @Test
    void nanoTimeOverflowDoesNotResetTheBudget() {
        var nanos = new AtomicLong(Long.MAX_VALUE - 3);
        var deadline = PreviewDeadline.after(Duration.ofNanos(10), nanos::get);
        nanos.addAndGet(8);
        assertThat(deadline.remaining()).isEqualTo(Duration.ofNanos(2));
        nanos.addAndGet(3);
        assertThatThrownBy(deadline::check).hasMessage("PREVIEW_TIMEOUT");
    }

    @Test
    void cancellationIsSharedAndDoesNotResetTime() {
        var deadline = PreviewDeadline.after(Duration.ofSeconds(18));
        deadline.cancel();
        assertThatThrownBy(deadline::remaining).hasMessage("CANCELLED");
        assertThatThrownBy(deadline::check).hasMessage("CANCELLED");
    }

    @Test
    void callerInterruptionRemainsSet() {
        var deadline = PreviewDeadline.after(Duration.ofSeconds(18));
        try {
            Thread.currentThread().interrupt();
            assertThatThrownBy(deadline::check).hasMessage("CANCELLED");
            assertThat(Thread.currentThread().isInterrupted()).isTrue();
        } finally {
            Thread.interrupted();
        }
    }

    @Test
    void childBudgetExpiresWithoutCancellingTheParent() {
        var nanos = new AtomicLong();
        var parent = PreviewDeadline.after(Duration.ofSeconds(18), nanos::get);
        var child = parent.within(Duration.ofMillis(500));
        nanos.addAndGet(Duration.ofMillis(500).toNanos());
        assertThatThrownBy(child::check).hasMessage("PREVIEW_TIMEOUT");
        assertThat(parent.remaining()).isEqualTo(Duration.ofMillis(17500));
    }

    @Test
    void childNeverOutlivesTheParentOrIgnoresParentCancellation() {
        var nanos = new AtomicLong();
        var parent = PreviewDeadline.after(Duration.ofSeconds(1), nanos::get);
        var child = parent.within(Duration.ofSeconds(3));
        assertThat(child.remaining()).isEqualTo(Duration.ofSeconds(1));
        parent.cancel();
        assertThatThrownBy(child::check).hasMessage("CANCELLED");
    }
}
