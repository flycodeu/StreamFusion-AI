package com.streamfusion.platform.media.preview.media;

import static org.assertj.core.api.Assertions.assertThat;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;

class PreviewAudioTracksTest {
    private final ObjectMapper json = new ObjectMapper();

    @Test
    void onlyObservedReadySingleBaselineAudioIsCompatible() throws Exception {
        for (String codec : new String[] {"PCMA", "PCMU", "G.711A", "g711u", "opus", "G-711A"}) {
            assertThat(
                            PreviewAudioTracks.compatible(
                                    json.readTree(
                                            "[{\"tracks\":[{\"codec_type\":0,\"ready\":true,\"codec_id_name\":\"H264\"},"
                                                    + "{\"codec_type\":1,\"ready\":true,\"codec_id_name\":\""
                                                    + codec
                                                    + "\"}]}]")))
                    .as(codec)
                    .isTrue();
        }
    }

    @Test
    void unknownUnsupportedUnreadyAndAmbiguousAudioStayVideoOnly() throws Exception {
        for (String media :
                new String[] {
                    "null",
                    "{}",
                    "[]",
                    "[{},{}]",
                    "[{}]",
                    "[{\"tracks\":{}}]",
                    "[{\"tracks\":[{\"codec_type\":0,\"ready\":true,\"codec_id_name\":\"H264\"}]}]",
                    "[{\"tracks\":[{\"codec_type\":1,\"ready\":true,\"codec_id_name\":\"AAC\"}]}]",
                    "[{\"tracks\":[{\"codec_type\":1,\"ready\":false,\"codec_id_name\":\"PCMA\"}]}]",
                    "[{\"tracks\":[{\"codec_type\":1,\"ready\":\"true\",\"codec_id_name\":\"PCMA\"}]}]",
                    "[{\"tracks\":[{\"codec_type\":\"1\",\"ready\":true,\"codec_id_name\":\"PCMA\"}]}]",
                    "[{\"tracks\":[{\"codec_type\":1,\"ready\":true}]}]",
                    "[{\"tracks\":[{\"codec_type\":1,\"ready\":true,\"codec_id_name\":\"PCMA\"},{\"codec_type\":1,\"ready\":true,\"codec_id_name\":\"OPUS\"}]}]"
                })
            assertThat(PreviewAudioTracks.compatible(json.readTree(media))).as(media).isFalse();
        assertThat(PreviewAudioTracks.compatible(null)).isFalse();
    }
}
