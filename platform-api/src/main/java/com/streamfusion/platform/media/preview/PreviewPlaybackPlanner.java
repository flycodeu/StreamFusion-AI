package com.streamfusion.platform.media.preview;

import java.util.Collection;
import java.util.Locale;

/** Codec selection only; resource reservation and browser evidence remain separate concerns. */
public final class PreviewPlaybackPlanner {
    public enum Decision {
        DIRECT,
        TRANSCODE_H264,
        UNSUPPORTED
    }

    private PreviewPlaybackPlanner() {}

    public static Decision decide(String actualCodec, Collection<String> browserCodecs) {
        String codec = normalize(actualCodec);
        if (!"H264".equals(codec) && !"H265".equals(codec)) return Decision.UNSUPPORTED;
        if (browserCodecs.contains(codec)) return Decision.DIRECT;
        if ("H265".equals(codec) && browserCodecs.contains("H264")) return Decision.TRANSCODE_H264;
        return Decision.UNSUPPORTED;
    }

    public static String normalize(String codec) {
        if (codec == null) return "";
        String value = codec.toUpperCase(Locale.ROOT).replace(".", "");
        return value.equals("AVC") ? "H264" : value.equals("HEVC") ? "H265" : value;
    }
}
