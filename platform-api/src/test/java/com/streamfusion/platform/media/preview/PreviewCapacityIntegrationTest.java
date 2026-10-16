package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.streamfusion.platform.media.preview.media.PreviewTranscodeRegistry;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.ArrayList;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

/** Real HTTP identity/module checks; media ownership is isolated from the installed journal. */
class PreviewCapacityIntegrationTest extends CameraTestSupport {
    @MockitoBean PreviewTranscodeRegistry registry;

    @Test
    void exposesOnlyAggregateCapacityWithUnknownJournalReportedHonestly() throws Exception {
        var auth = admin();
        when(registry.occupied()).thenReturn(2);
        var result = read("/preview/capacity", auth);
        var fields = new ArrayList<String>();
        result.fieldNames().forEachRemaining(fields::add);
        assertThat(fields)
                .containsExactlyInAnyOrder(
                        "maxSessions",
                        "occupiedSessions",
                        "closingSessions",
                        "maxTranscodes",
                        "occupiedTranscodes",
                        "transcodesAvailable",
                        "observedAt");
        assertThat(result.path("maxSessions").asInt()).isEqualTo(36);
        assertThat(result.path("occupiedSessions").asInt()).isZero();
        assertThat(result.path("occupiedTranscodes").asInt()).isEqualTo(2);
        assertThat(result.path("transcodesAvailable").asBoolean()).isTrue();
        assertThat(result.path("observedAt").asText()).isNotEmpty();
        when(registry.occupied()).thenThrow(new PreviewFailure("TRANSCODE_RECOVERY_REQUIRED"));
        var unknown = read("/preview/capacity", auth);
        assertThat(unknown.path("occupiedTranscodes").isNull()).isTrue();
        assertThat(unknown.path("transcodesAvailable").asBoolean()).isFalse();
    }

    @Test
    void anonymousAndRevokedUsersCannotReadGlobalUsage() throws Exception {
        mvc.perform(get("/preview/capacity")).andExpect(status().isUnauthorized());
        var auth = admin();
        String user =
                write(post("/user"), auth, Map.of("username", "CapacityWorker"), 201)
                        .path("id")
                        .asText();
        jdbc.update("UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?", user);
        var worker = login("CapacityWorker", "Initial1!");
        mvc.perform(get("/preview/capacity").session(worker.session()))
                .andExpect(status().isForbidden());
        verifyNoInteractions(registry);
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8901,'CAPACITY_TEST','容量测试','ENABLED',0)");
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8901)", user);
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8901,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        assertThat(read("/preview/capacity", worker).path("maxSessions").asInt()).isEqualTo(36);
        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=8901");
        mvc.perform(get("/preview/capacity").session(worker.session()))
                .andExpect(status().isForbidden());
    }
}
