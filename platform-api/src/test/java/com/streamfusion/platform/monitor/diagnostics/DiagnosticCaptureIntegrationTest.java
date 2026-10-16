package com.streamfusion.platform.monitor.diagnostics;

import static org.assertj.core.api.Assertions.*;
import static org.awaitility.Awaitility.await;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.streamfusion.platform.support.CameraTestSupport;
import java.util.*;
import org.junit.jupiter.api.Test;

class DiagnosticCaptureIntegrationTest extends CameraTestSupport {
    @Test
    void httpQueryBindsPageSizeAndCursorAndRetainsAuthentication() throws Exception {
        var auth = admin();
        String trace = UUID.randomUUID().toString().replace("-", "");
        for (int i = 0; i < 2; i++)
            mvc.perform(
                            get("/camera-status/plans")
                                    .session(auth.session())
                                    .header("X-Trace-Id", trace))
                    .andExpect(status().isOk());
        await().atMost(java.time.Duration.ofSeconds(3))
                .until(
                        () ->
                                read("/diagnostics/events?traceId=" + trace, auth)
                                                .path("items")
                                                .size()
                                        == 2);
        var first = read("/diagnostics/events?traceId=" + trace + "&size=1", auth);
        assertThat(first.path("limit").asInt()).isEqualTo(1);
        assertThat(first.path("items").size()).isEqualTo(1);
        assertThat(first.path("truncated").asBoolean()).isFalse();
        assertThat(first.path("nextCursor").asText()).isNotBlank();
        var second =
                read(
                        "/diagnostics/events?traceId="
                                + trace
                                + "&size=1&cursor="
                                + first.path("nextCursor").asText(),
                        auth);
        assertThat(second.path("items").size()).isEqualTo(1);
        assertThat(second.path("cursorExpired").asBoolean()).isFalse();
        assertThat(second.path("items").get(0).path("occurredAt"))
                .isNotEqualTo(first.path("items").get(0).path("occurredAt"));
        mvc.perform(get("/diagnostics/events")).andExpect(status().isUnauthorized());
        mvc.perform(get("/diagnostics/events?size=201").session(auth.session()))
                .andExpect(status().isBadRequest());
        mvc.perform(get("/diagnostics/events?cursor=../secret").session(auth.session()))
                .andExpect(status().isBadRequest());
    }

    @Test
    void captureIsOffByDefaultThenCapturesSafeRequestAndResponseWithoutChangingFailure()
            throws Exception {
        var auth = admin();
        String trace = UUID.randomUUID().toString().replace("-", "");
        mvc.perform(get("/camera-status/plans").session(auth.session()).header("X-Trace-Id", trace))
                .andExpect(status().isOk());
        assertThat(event(trace, auth).path("request").isNull()).isTrue();
        write(
                post("/diagnostics/capture"),
                auth,
                Map.of(
                        "routes",
                        List.of("POST /camera-status/plans"),
                        "minutes",
                        15,
                        "errorsOnly",
                        false),
                200);
        trace = UUID.randomUUID().toString().replace("-", "");
        mvc.perform(
                        post("/camera-status/plans")
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("X-Trace-Id", trace)
                                .contentType("application/json")
                                .content(
                                        "{\"clientRequestId\":\"safe-id\",\"groupKeys\":[],\"intervalMinutes\":60,\"durationHours\":24,\"password\":\"do-not-record\",\"credentials\":{\"secret\":\"nested-secret\"}}"))
                .andExpect(status().isBadRequest());
        var event = event(trace, auth);
        assertThat(event.path("request").path("body").path("intervalMinutes").asInt())
                .isEqualTo(60);
        assertThat(event.path("response").path("code").asText()).isEqualTo("VALIDATION_ERROR");
        assertThat(event.toString())
                .doesNotContain("do-not-record", "nested-secret", "password", "credentials");
        write(delete("/diagnostics/capture"), auth, Map.of(), 200);
    }

    @Test
    void invalidJsonIsClassifiedWithoutSavingRawSecretsAndSensitiveRoutesAreRejected()
            throws Exception {
        var auth = admin();
        write(
                post("/diagnostics/capture"),
                auth,
                Map.of("routes", List.of("POST /auth/login"), "minutes", 15, "errorsOnly", false),
                400);
        write(
                post("/diagnostics/capture"),
                auth,
                Map.of(
                        "routes",
                        List.of("POST /camera-status/plans"),
                        "minutes",
                        15,
                        "errorsOnly",
                        true),
                200);
        String trace = UUID.randomUUID().toString().replace("-", "");
        mvc.perform(
                        post("/camera-status/plans")
                                .session(auth.session())
                                .header(auth.header(), auth.token())
                                .header("X-Trace-Id", trace)
                                .contentType("application/json")
                                .content("{\"password\":\"bad-json-secret\",garbage"))
                .andExpect(status().isBadRequest());
        var event = event(trace, auth);
        assertThat(event.path("request").path("state").asText()).isEqualTo("INVALID_JSON");
        assertThat(event.toString()).doesNotContain("bad-json-secret");
        write(delete("/diagnostics/capture"), auth, Map.of(), 200);
    }

    private com.fasterxml.jackson.databind.JsonNode event(String trace, Csrf auth) {
        return await().atMost(java.time.Duration.ofSeconds(3))
                .until(
                        () ->
                                read("/diagnostics/events?traceId=" + trace, auth)
                                        .path("items")
                                        .get(0),
                        java.util.Objects::nonNull);
    }
}
