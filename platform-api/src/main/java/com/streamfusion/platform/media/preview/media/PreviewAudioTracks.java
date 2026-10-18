package com.streamfusion.platform.media.preview.media;

import com.fasterxml.jackson.databind.JsonNode;
import java.util.Locale;
import java.util.Set;

/** Only observed, ready WebRTC baseline audio is forwarded; unknown codecs stay video-only. */
public final class PreviewAudioTracks {
    private static final Set<String> CODECS = Set.of("PCMA", "PCMU", "G711A", "G711U", "OPUS");

    private PreviewAudioTracks() {}

    public static boolean compatible(JsonNode media) {
        if (media == null || !media.isArray() || media.size() != 1) return false;
        JsonNode tracks = media.get(0).path("tracks");
        if (!tracks.isArray()) return false;
        JsonNode audio = null;
        for (JsonNode track : tracks) {
            if (!track.path("codec_type").isInt() || track.path("codec_type").intValue() != 1)
                continue;
            if (audio != null) return false;
            audio = track;
        }
        return audio != null
                && audio.path("ready").isBoolean()
                && audio.path("ready").booleanValue()
                && CODECS.contains(
                        audio.path("codec_id_name")
                                .asText()
                                .toUpperCase(Locale.ROOT)
                                .replace(".", "")
                                .replace("-", ""));
    }
}
