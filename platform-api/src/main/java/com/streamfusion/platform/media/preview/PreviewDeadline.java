package com.streamfusion.platform.media.preview;

import java.time.Duration;
import java.util.Objects;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.LongSupplier;

/** One monotonic budget shared by every step of a preview operation. */
public final class PreviewDeadline {
    private final LongSupplier ticker;
    private final long startedAt;
    private final long budgetNanos;
    private final PreviewDeadline parent;
    private final AtomicBoolean cancelled = new AtomicBoolean();

    private PreviewDeadline(Duration budget, LongSupplier ticker) {
        this(budget, ticker, null);
    }

    private PreviewDeadline(Duration budget, LongSupplier ticker, PreviewDeadline parent) {
        if (budget.isNegative() || budget.isZero())
            throw new IllegalArgumentException("Preview budget must be positive");
        this.ticker = Objects.requireNonNull(ticker);
        this.startedAt = ticker.getAsLong();
        this.budgetNanos = budget.toNanos();
        this.parent = parent;
    }

    public static PreviewDeadline after(Duration budget) {
        return after(budget, System::nanoTime);
    }

    static PreviewDeadline after(Duration budget, LongSupplier ticker) {
        return new PreviewDeadline(budget, ticker);
    }

    public PreviewDeadline within(Duration maximum) {
        Duration available = remaining();
        return new PreviewDeadline(
                maximum.compareTo(available) < 0 ? maximum : available, ticker, this);
    }

    public Duration remaining() {
        if (cancelled.get() || Thread.currentThread().isInterrupted())
            throw new PreviewFailure("CANCELLED");
        long remaining = budgetNanos - (ticker.getAsLong() - startedAt);
        if (remaining <= 0) throw new PreviewFailure("PREVIEW_TIMEOUT");
        Duration ownRemaining = Duration.ofNanos(remaining);
        if (parent == null) return ownRemaining;
        Duration parentRemaining = parent.remaining();
        return ownRemaining.compareTo(parentRemaining) < 0 ? ownRemaining : parentRemaining;
    }

    public void check() {
        remaining();
    }

    public long deadlineNanos() {
        return System.nanoTime() + remaining().toNanos();
    }

    public void cancel() {
        cancelled.set(true);
    }
}
