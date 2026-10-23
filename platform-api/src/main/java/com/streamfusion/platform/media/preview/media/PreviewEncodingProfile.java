package com.streamfusion.platform.media.preview.media;

/** Shared encoder profile used by configuration examples and the dedicated node renderer. */
public final class PreviewEncodingProfile {
    private PreviewEncodingProfile() {}

    public static String software() {
        return command(
                "libx264 -preset veryfast -tune zerolatency -threads 4 -crf 23 -maxrate 6M -bufsize 3M");
    }

    private static String command(String video) {
        // Optional audio mapping preserves video-only cameras without inventing a silent track.
        return "%s -hide_banner -nostdin -nostats -rtsp_transport tcp -timeout 10000000 -threads 2 -i %s -map 0:v:0 -map 0:a:0? -c:v "
                + video
                + " -pix_fmt yuv420p -profile:v baseline -bf 0 -g 40 -keyint_min 20 -sc_threshold 0 -c:a libopus -ar 48000 -ac 1 -b:a 64k -f rtsp -rtsp_transport tcp %s";
    }

    public static boolean carriesAudio(String command) {
        return command != null
                && command.contains("-map 0:a:0?")
                && command.contains("-c:a libopus")
                && command.contains("-f rtsp")
                && !command.matches("(?s).*(?:^|\\s)-an(?:\\s|$).*");
    }
}
