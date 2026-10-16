package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;

import java.util.List;
import java.util.stream.Stream;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.Arguments;
import org.junit.jupiter.params.provider.MethodSource;

class PreviewPlaybackPlannerTest {
    @ParameterizedTest
    @MethodSource("decisions")
    void codecSelectionIsExplicit(
            String actual, List<String> browser, PreviewPlaybackPlanner.Decision expected) {
        assertThat(PreviewPlaybackPlanner.decide(actual, browser)).isEqualTo(expected);
    }

    static Stream<Arguments> decisions() {
        var direct = PreviewPlaybackPlanner.Decision.DIRECT;
        var transcode = PreviewPlaybackPlanner.Decision.TRANSCODE_H264;
        var unsupported = PreviewPlaybackPlanner.Decision.UNSUPPORTED;
        return Stream.of(
                Arguments.of("H264", List.of("H264"), direct),
                Arguments.of("AVC", List.of("H264"), direct),
                Arguments.of("h.264", List.of("H264", "H265"), direct),
                Arguments.of("H265", List.of("H264", "H265"), direct),
                Arguments.of("HEVC", List.of("H265"), direct),
                Arguments.of("h.265", List.of("H264"), transcode),
                Arguments.of("H264", List.of("H265"), unsupported),
                Arguments.of("AV1", List.of("H264", "H265"), unsupported),
                Arguments.of("MJPEG", List.of("MJPEG"), unsupported),
                Arguments.of(null, List.of("H264"), unsupported));
    }
}
