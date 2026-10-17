package com.streamfusion.platform.media.preview.execution;

import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveFfmpeg;
import com.streamfusion.platform.media.runtime.service.MediaRuntimeService.ActiveZlm;
import java.util.Optional;

/** Only trusted local runtimes; discovery and platform configuration stay outside execution. */
public interface PreviewRuntimeProvider {
    Optional<ActiveZlm> activeZlm();

    Optional<ActiveFfmpeg> activeFfmpeg();

    Optional<ActiveZlm> recoveryZlm(String api, String configPath);
}
