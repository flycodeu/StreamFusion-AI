package com.streamfusion.platform.monitor.diagnostics;

import static org.assertj.core.api.Assertions.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.*;
import org.junit.jupiter.api.Test;

class PayloadSanitizerTest {
    final ObjectMapper json = new ObjectMapper().findAndRegisterModules();
    final PayloadSanitizer sanitizer = new PayloadSanitizer(json);

    @Test
    void excludesNestedSecretsUrlsAndPreservesNumericTypes() {
        var node =
                sanitizer.project(
                        Map.of(
                                "data",
                                Map.of(
                                        "name",
                                        "rtsp://u:secret@host/path",
                                        "password",
                                        "hidden",
                                        "items",
                                        List.of(Map.of("id", "4", "token", "hidden-token"))),
                                "intervalMinutes",
                                60));
        assertThat(node.path("intervalMinutes").isNumber()).isTrue();
        assertThat(node.toString()).doesNotContain("hidden", "hidden-token", "rtsp://", "secret");
        assertThat(node.path("data").path("items").get(0).path("id").asText()).isEqualTo("4");
    }

    @Test
    void boundsCollectionDepthAndFinalBytes() throws Exception {
        var node =
                sanitizer.project(
                        Map.of(
                                "items",
                                Collections.nCopies(10000, Map.of("name", "长".repeat(10000)))));
        assertThat(node.path("items").size()).isLessThanOrEqualTo(21);
        assertThat(json.writeValueAsBytes(node).length).isLessThanOrEqualTo(8192);
    }

    @Test
    void projectionFailureIsOmittedRatherThanBreakingTheBusinessResponse() {
        Map<String, Object> unreadable =
                new AbstractMap<>() {
                    @Override
                    public Set<Entry<String, Object>> entrySet() {
                        throw new IllegalStateException("sensitive-source-failure");
                    }
                };
        assertThat(sanitizer.project(unreadable).toString())
                .contains("captureNotice")
                .doesNotContain("sensitive-source-failure");
    }
}
