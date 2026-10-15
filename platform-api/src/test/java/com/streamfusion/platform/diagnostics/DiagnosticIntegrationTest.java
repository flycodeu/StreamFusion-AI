package com.streamfusion.platform.diagnostics;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.streamfusion.platform.support.CameraTestSupport;
import org.junit.jupiter.api.Test;

class DiagnosticIntegrationTest extends CameraTestSupport {
    @Test
    void diagnosticModuleHasIndependentPermissionAndRequestCorrelation() throws Exception {
        mvc.perform(get("/diagnostics/events")).andExpect(status().isUnauthorized());
        var auth = admin();
        String trace = "d".repeat(32);
        mvc.perform(get("/camera-status/plans").session(auth.session()).header("X-Trace-Id", trace))
                .andExpect(status().isOk());
        var result = read("/diagnostics/events?traceId=" + trace, auth);
        assertThat(result.path("items").toString()).contains(trace, "/camera-status/plans");
        mvc.perform(
                        get("/diagnostics/events")
                                .session(auth.session())
                                .param("startTime", "2026-01-01T00:00:00Z")
                                .param("endTime", "2026-01-15T00:00:00Z"))
                .andExpect(status().isBadRequest());
        jdbc.update("UPDATE sys_menu SET enabled=FALSE WHERE module_key='diagnostics'");
        mvc.perform(get("/diagnostics/events").session(auth.session()))
                .andExpect(status().isForbidden());
        mvc.perform(get("/audit/page").session(auth.session())).andExpect(status().isOk());
    }
}
