package com.streamfusion.platform.monitor.diagnostics;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.streamfusion.platform.support.CameraTestSupport;
import java.util.*;
import org.junit.jupiter.api.Test;

class DiagnosticCaptureIntegrationTest extends CameraTestSupport {
    @Test
    void captureIsOffByDefaultThenCapturesSafeRequestAndResponseWithoutChangingFailure()
            throws Exception {
        var auth = admin();
        String trace = UUID.randomUUID().toString().replace("-", "");
        mvc.perform(get("/camera-status/plans").session(auth.session()).header("X-Trace-Id", trace))
                .andExpect(status().isOk());
        assertThat(
                        read("/diagnostics/events?traceId=" + trace, auth)
                                .path("items")
                                .get(0)
                                .path("request")
                                .isNull())
                .isTrue();
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
        var event = read("/diagnostics/events?traceId=" + trace, auth).path("items").get(0);
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
        var event = read("/diagnostics/events?traceId=" + trace, auth).path("items").get(0);
        assertThat(event.path("request").path("state").asText()).isEqualTo("INVALID_JSON");
        assertThat(event.toString()).doesNotContain("bad-json-secret");
        write(delete("/diagnostics/capture"), auth, Map.of(), 200);
    }
}
