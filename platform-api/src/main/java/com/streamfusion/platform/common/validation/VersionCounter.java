package com.streamfusion.platform.common.validation;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;

/** Monotonic versions must never wrap or reuse a previously issued concurrency token. */
public final class VersionCounter {
    private VersionCounter() {}

    public static void requireIncrementable(long version) {
        if (version < 0 || version == Long.MAX_VALUE)
            throw BusinessException.error(ErrorCode.VERSION_EXHAUSTED);
    }

    public static long next(long version) {
        requireIncrementable(version);
        return version + 1;
    }
}
