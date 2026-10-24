package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.status.CameraStatusCheckMapper;
import com.streamfusion.platform.camera.status.CameraStatusSnapshotMapper;
import com.streamfusion.platform.camera.status.CameraStatusWorker;
import com.streamfusion.platform.support.CameraTestSupport;
import java.time.LocalDateTime;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.stream.IntStream;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;

/** Isolated H2 correctness/batch-boundary baseline, not MySQL throughput or camera acceptance. */
class CameraScaleIntegrationTest extends CameraTestSupport {
    private static final int CHANNELS = 5000;
    private static final long ROOT = 91001;
    private static final long VISIBLE = 91002;
    private static final long HIDDEN = 91003;
    private static final int DIRECT = 3000;
    @Autowired CameraCryptoService crypto;
    @MockitoBean CameraStatusWorker worker;
    @MockitoSpyBean CameraProfileMapper profiles;
    @MockitoSpyBean CameraStatusSnapshotMapper snapshots;
    @MockitoSpyBean CameraStatusCheckMapper checks;

    @Test
    void fiveThousandChannelsKeepPagingCountsAndScopeBoundedWithoutExecutingChecks()
            throws Exception {
        try (var connection = dataSource.getConnection()) {
            assertThat(connection.getMetaData().getDatabaseProductName()).isEqualTo("H2");
        }
        var auth = admin();
        long started = System.nanoTime();
        seed(Long.parseLong(source(auth).path("sourceId").asText()));
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isEqualTo(CHANNELS);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_stream_profile", Integer.class))
                .isEqualTo(CHANNELS * 2);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_status_snapshot", Integer.class))
                .isEqualTo(CHANNELS);

        var catalog = timed("admin catalog tail", "/preview/catalog?page=50&size=100", auth);
        assertPage(catalog, CHANNELS, 100);
        assertThat(catalog.path("items").get(0).path("cameraId").asText())
                .isEqualTo(Long.toString(cameraId(99)));
        assertThat(catalog.path("items").get(99).path("cameraId").asText())
                .isEqualTo(Long.toString(cameraId(0)));
        for (var item : catalog.path("items")) {
            assertThat(item.path("profiles")).hasSize(2);
            assertThat(item.path("profiles").get(0).path("configured").asBoolean()).isTrue();
        }
        assertPage(read("/preview/catalog?page=51&size=100", auth), CHANNELS, 0);
        assertPage(
                status("/camera-status?layer=CONTROL&page=50&size=100", auth, 100), CHANNELS, 100);
        var filtered =
                status("/camera-status?layer=CONTROL&observation=FAILED&size=100", auth, 100);
        assertPage(filtered, 2500, 100);
        // These are synthetic historical facts: an old fact is not renewed by viewing the page.
        assertThat(filtered.path("summary").path("stale").asInt()).isEqualTo(100);
        assertGroupCounts(read("/camera-groups/tree", auth), 5000, 2500, 2500);

        String user =
                write(post("/user"), auth, Map.of("username", "ScaleViewer"), 201)
                        .path("id")
                        .asText();
        jdbc.update("UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?", user);
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8911,'SCALE_VIEW','规模测试','ENABLED',0)");
        jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8911)", user);
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8911,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupIds",
                        List.of(Long.toString(VISIBLE)),
                        "cameraIds",
                        List.of(Long.toString(cameraId(DIRECT)))),
                200);
        var viewer = login("ScaleViewer", "Initial1!");
        var scoped = timed("scoped catalog tail", "/preview/catalog?page=26&size=100", viewer);
        assertPage(scoped, 2501, 1);
        for (var item : read("/preview/catalog?size=100", viewer).path("items"))
            assertVisible(item);
        var scopedStatus = status("/camera-status?layer=CONTROL&size=100", viewer, 100);
        assertPage(scopedStatus, 2501, 100);
        for (var item : scopedStatus.path("items")) assertVisible(item);
        assertPage(
                status("/camera-status?layer=CONTROL&observation=FAILED&size=100", viewer, 1),
                1,
                1);
        assertPage(read("/preview/catalog?name=scale-hidden-4000", viewer), 0, 0);
        assertPage(status("/camera-status?layer=CONTROL&name=scale-hidden-4000", viewer, 0), 0, 0);
        assertGroupCounts(read("/camera-groups/tree", viewer), 2501, 2500, 1);

        var oversized = IntStream.range(0, 2001).mapToObj(index -> "c" + cameraId(index)).toList();
        write(
                post("/camera-status/plans"),
                auth,
                Map.of(
                        "clientRequestId",
                        requestId(),
                        "groupKeys",
                        oversized,
                        "intervalMinutes",
                        1440,
                        "durationHours",
                        24),
                400);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_status_plan", Integer.class))
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_status_check", Integer.class))
                .isZero();
        verifyNoInteractions(worker);

        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of()),
                200);
        assertPage(read("/preview/catalog?size=100", viewer), 0, 0);
        assertPage(status("/camera-status?layer=CONTROL&size=100", viewer, 0), 0, 0);
        assertThat(read("/camera-groups/tree", viewer)).isEmpty();
        verifyNoInteractions(worker);
        System.out.printf(
                "CameraScale H2 5000 channels/10000 profiles correctness totalMs=%d (not MySQL capacity)%n",
                (System.nanoTime() - started) / 1_000_000);
    }

    private JsonNode status(String path, Csrf auth, int pageSize) throws Exception {
        clearInvocations(profiles, snapshots, checks);
        var page = timed("status page", path, auth);
        if (pageSize == 0) {
            verify(profiles, never()).statusProfiles(anyList(), anyInt());
            verify(snapshots, never()).forProfiles(anyList());
            verify(checks, never()).latestForProfiles(anyList());
        } else {
            verify(profiles).statusProfiles(argThat(ids -> ids.size() == pageSize), eq(3201));
            verify(snapshots).forProfiles(argThat(ids -> ids.size() == pageSize * 2));
            verify(checks).latestForProfiles(argThat(ids -> ids.size() == pageSize * 2));
        }
        return page;
    }

    private JsonNode timed(String label, String path, Csrf auth) throws Exception {
        long start = System.nanoTime();
        var result = read(path, auth);
        System.out.printf(
                "CameraScale H2 %s elapsedMs=%d%n", label, (System.nanoTime() - start) / 1_000_000);
        return result;
    }

    private static void assertPage(JsonNode page, int total, int count) {
        assertThat(page.path("total").asInt()).isEqualTo(total);
        assertThat(page.path("items")).hasSize(count);
        assertThat(page.toString())
                .doesNotContain("secretCiphertext", "connectionDigest", "rtsp://");
    }

    private static void assertVisible(JsonNode item) {
        long id = item.path("cameraId").asLong();
        assertThat(id >= cameraId(0) && id < cameraId(2500) || id == cameraId(DIRECT)).isTrue();
    }

    private static void assertGroupCounts(JsonNode tree, int root, int visible, int hidden) {
        assertThat(tree).hasSize(3);
        var expected = Map.of(ROOT, root, VISIBLE, visible, HIDDEN, hidden);
        for (var group : tree)
            assertThat(group.path("visibleCameraCount").asInt())
                    .isEqualTo(expected.get(group.path("groupId").asLong()));
    }

    private void seed(long sourceId) {
        jdbc.update("INSERT INTO camera_group(id,name,version) VALUES(?,'Scale root',0)", ROOT);
        jdbc.update(
                "INSERT INTO camera_group(id,parent_id,name,version) VALUES(?,?,'Visible child',0)",
                VISIBLE,
                ROOT);
        jdbc.update(
                "INSERT INTO camera_group(id,parent_id,name,version) VALUES(?,?,'Hidden child',0)",
                HIDDEN,
                ROOT);
        var channelRows = new ArrayList<Object[]>();
        var profileRows = new ArrayList<Object[]>();
        var locatorRows = new ArrayList<Object[]>();
        var factRows = new ArrayList<Object[]>();
        LocalDateTime historical = LocalDateTime.of(2020, 1, 1, 0, 0);
        for (int index = 0; index < CHANNELS; index++) {
            boolean visible = index < 2500;
            // Unknown stream metrics fall back to SUB for detection, independently of preview.
            long camera = cameraId(index), detectionProfile = profileId(index, 1);
            String name =
                    "scale-" + (visible ? "visible-" : "hidden-") + String.format("%04d", index);
            channelRows.add(
                    new Object[] {
                        camera,
                        sourceId,
                        "scale-" + index,
                        visible ? VISIBLE : HIDDEN,
                        name,
                        historical
                    });
            for (int stream = 0; stream < 2; stream++) {
                long profile = profileId(index, stream);
                String kind = stream == 0 ? "MAIN" : "SUB";
                profileRows.add(new Object[] {profile, sourceId, camera, kind, kind, kind});
                var envelope =
                        crypto.encrypt(
                                "camera_profile_locator/" + sourceId + "/" + profile + "/RTSP",
                                Map.of(
                                        "encodedPath",
                                        "/scale-" + index + "/" + kind,
                                        "rawQuery",
                                        ""),
                                16000);
                locatorRows.add(
                        new Object[] {
                            profile,
                            sourceId,
                            envelope.ciphertext(),
                            envelope.nonce(),
                            envelope.tag(),
                            envelope.keyId(),
                            crypto.digest(crypto.activeKeyId(), "scale-test-locator", profile),
                            historical,
                            historical
                        });
            }
            factRows.add(
                    new Object[] {
                        detectionProfile,
                        camera,
                        visible ? "ACCESSIBLE" : "FAILED",
                        visible ? "RTSP_DESCRIBE_OK" : "RTSP_CONNECT_FAILED",
                        historical,
                        historical,
                        historical.plusMinutes(5),
                        historical,
                        historical,
                        detectionProfile,
                        crypto.activeKeyId(),
                        new byte[32]
                    });
        }
        jdbc.batchUpdate(
                "INSERT INTO camera_channel(id,source_id,external_channel_key,group_id,name,created_at,lifecycle,mapping_origin,version) VALUES(?,?,?,?,?,?,'ENABLED','MANUAL',0)",
                channelRows);
        jdbc.batchUpdate(
                "INSERT INTO camera_stream_profile(id,source_id,channel_id,external_profile_key,label,usage_hint,usage_origin,enabled,version) VALUES(?,?,?,?,?,?,'MANUAL',TRUE,0)",
                profileRows);
        jdbc.batchUpdate(
                "INSERT INTO camera_profile_locator(profile_id,source_id,locator_kind,endpoint_purpose,rtsp_host_mode,rtsp_transport,rtsp_secret_ciphertext,rtsp_secret_nonce,rtsp_secret_tag,rtsp_encryption_key_id,identity_digest,created_at,updated_at) VALUES(?,?,'RTSP','RTSP','SOURCE','TCP',?,?,?,?,?,?,?)",
                locatorRows);
        jdbc.batchUpdate(
                "INSERT INTO camera_status_snapshot(profile_id,camera_id,layer,probe_kind,observation_value,reason_code,observed_at,read_at,valid_until,check_requested_at,updated_at,check_id,connection_key_id,connection_digest) VALUES(?,?,'CONTROL','RTSP_DESCRIBE',?,?,?,?,?,?,?,?,?,?)",
                factRows);
    }

    private static long cameraId(int index) {
        return 1_000_000L + index;
    }

    private static long profileId(int index, int stream) {
        return 2_000_000L + index * 2L + stream;
    }
}
