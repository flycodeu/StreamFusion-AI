package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.support.CameraTestSupport;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;

/** Real auth/HTTP/SQL pagination, including facts with stale or changed configurations. */
class CameraStatusPageIntegrationTest extends CameraTestSupport {
    @MockitoBean private CameraStatusWorker worker;
    @MockitoSpyBean private CameraStatusSnapshotMapper snapshots;
    @MockitoSpyBean private CameraStatusCheckMapper checks;
    @Autowired private CameraChannelMapper channels;
    @MockitoSpyBean private CameraProfileMapper profiles;
    @MockitoSpyBean private CameraRtspTargetService targets;

    @Test
    void requestedLayerControlsHistoryFilteringAndPageSummary() throws Exception {
        var auth = admin();
        var camera = camera(auth, source(auth).path("sourceId").asText(), "分层筛选");
        enable(auth, camera);
        addFact(camera, 0, "FAILED", false);
        var media =
                snapshots.get(
                        camera.path("profiles").get(0).path("streamProfileId").asLong(), "CONTROL");
        media.setLayer("MEDIA");
        media.setProbeKind("ZLM_RTP_FRAMES");
        media.setValue("ACCESSIBLE");
        media.setReasonCode("MEDIA_RECEIVING");
        media.setMediaWindowStartedAt(media.getObservedAt().minusSeconds(5));
        media.setMediaWindowEndedAt(media.getObservedAt());
        media.setMediaFirstFrames(10L);
        media.setMediaLastFrames(85L);
        media.setMediaSampleCount(3);
        media.setMediaMaxGapMs(2500L);
        snapshots.insert(media);
        assertThat(
                        read("/camera-status?layer=CONTROL&observation=FAILED", auth)
                                .path("total")
                                .asInt())
                .isOne();
        assertThat(
                        read("/camera-status?layer=MEDIA&observation=FAILED", auth)
                                .path("total")
                                .asInt())
                .isZero();
        var page = read("/camera-status?layer=MEDIA&observation=ACCESSIBLE", auth);
        assertThat(page.path("total").asInt()).isOne();
        assertThat(page.path("summary").path("accessible").asInt()).isOne();
        assertThat(page.path("summary").path("failed").asInt()).isZero();
        assertThat(
                        page.path("items")
                                .get(0)
                                .path("profiles")
                                .get(0)
                                .path("media")
                                .path("observation")
                                .path("mediaEvidence")
                                .path("lastFrames")
                                .asLong())
                .isEqualTo(85);
        mvc.perform(get("/camera-status").session(auth.session()))
                .andExpect(status().isBadRequest());
        mvc.perform(get("/camera-status?layer=UNKNOWN").session(auth.session()))
                .andExpect(status().isBadRequest());
        verifyNoInteractions(worker);
    }

    @Test
    void emptyPageAndNoProfileChannelsAreNeverWithoutNetworkOrDatabaseWrites() throws Exception {
        var auth = admin();
        var empty = read("/camera-status?layer=CONTROL", auth);
        assertThat(empty.path("items")).isEmpty();
        assertThat(empty.path("total").asInt()).isZero();
        assertThat(empty.path("page").asInt()).isEqualTo(1);
        assertThat(empty.path("size").asInt()).isEqualTo(20);
        assertThat(empty.path("summary").path("total").asInt()).isZero();
        var source = source(auth).path("sourceId").asText();
        var body = new LinkedHashMap<>(cameraBody(source, "无码流通道", requestId()));
        body.put("profiles", List.of());
        body.remove("defaultProfileClientKey");
        var camera = write(post("/cameras"), auth, body, 201);
        var never = read("/camera-status?layer=CONTROL&observation=NEVER", auth);
        assertThat(never.path("items")).hasSize(1);
        assertThat(never.path("items").get(0).path("cameraId")).isEqualTo(camera.path("cameraId"));
        assertThat(never.path("items").get(0).path("groupKey").asText())
                .isEqualTo("c" + camera.path("cameraId").asText());
        assertThat(never.path("items").get(0).path("profiles")).isEmpty();
        assertThat(never.path("summary").path("total").asInt()).isOne();
        assertThat(never.path("summary").path("disabled").asInt()).isOne();
        assertThat(
                        read("/camera-status?layer=CONTROL&observation=ACCESSIBLE", auth)
                                .path("total")
                                .asInt())
                .isZero();
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_status_check", Integer.class))
                .isZero();
        verifyNoInteractions(worker);
    }

    @Test
    void summaryCountsCurrentPageRepresentativesWithoutTreatingOtherStreamsAsDeviceHealth()
            throws Exception {
        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var accessible = camera(auth, sourceId, "summary-accessible");
        var failed = camera(auth, sourceId, "summary-failed");
        var checking = camera(auth, sourceId, "summary-checking");
        var stale = camera(auth, sourceId, "summary-stale");
        var unknown = camera(auth, sourceId, "summary-unknown");
        camera(auth, sourceId, "summary-disabled");
        var unsupported = camera(auth, sourceId, "summary-unsupported");
        for (var camera : List.of(accessible, failed, checking, stale, unknown, unsupported))
            enable(auth, camera);
        addFact(accessible, 0, "ACCESSIBLE", false);
        addFact(accessible, 1, "FAILED", false);
        addFact(failed, 0, "FAILED", false);
        addFact(stale, 0, "ACCESSIBLE", true);
        addFact(unsupported, 0, "UNSUPPORTED", false);
        addCheck(checking, 0, 201L, LocalDateTime.now(ZoneId.of("Asia/Shanghai")));
        jdbc.update(
                "UPDATE camera_status_check SET execution_state='RUNNING',finished_at=NULL WHERE id=201");
        var summary = read("/camera-status?layer=CONTROL&name=summary-", auth).path("summary");
        assertThat(summary.path("total").asInt()).isEqualTo(7);
        assertThat(summary.path("accessible").asInt()).isOne();
        assertThat(summary.path("failed").asInt()).isOne();
        assertThat(summary.path("checking").asInt()).isOne();
        assertThat(summary.path("stale").asInt()).isOne();
        assertThat(summary.path("unknown").asInt()).isEqualTo(2);
        assertThat(summary.path("disabled").asInt()).isOne();
        var firstPage = read("/camera-status?layer=CONTROL&name=summary-&size=1", auth);
        assertThat(firstPage.path("total").asInt()).isEqualTo(7);
        assertThat(firstPage.path("summary").path("total").asInt()).isOne();
        verifyNoInteractions(worker);
    }

    @Test
    void summaryPrefersEnabledDefaultPreviewAndFallsBackToMainWhenDefaultIsDisabled()
            throws Exception {
        var auth = admin();
        var camera = camera(auth, source(auth).path("sourceId").asText(), "默认代表流");
        enable(auth, camera);
        addFact(camera, 0, "FAILED", false);
        addFact(camera, 1, "ACCESSIBLE", false);
        long defaultId = camera.path("profiles").get(1).path("streamProfileId").asLong();
        jdbc.update(
                "UPDATE camera_channel SET default_preview_profile_id=? WHERE id=?",
                defaultId,
                camera.path("cameraId").asLong());
        var page = read("/camera-status?layer=CONTROL", auth);
        assertThat(page.path("items").get(0).path("defaultPreviewProfileId").asText())
                .isEqualTo(Long.toString(defaultId));
        assertThat(page.path("summary").path("accessible").asInt()).isOne();
        assertThat(page.path("summary").path("failed").asInt()).isZero();
        jdbc.update("UPDATE camera_stream_profile SET enabled=FALSE WHERE id=?", defaultId);
        assertThat(
                        read("/camera-status?layer=CONTROL", auth)
                                .path("summary")
                                .path("failed")
                                .asInt())
                .isOne();
        verifyNoInteractions(worker);
    }

    @Test
    void filtersRepresentativeHistoryBeforePaginatingAndIgnoresUnobservedSecondaryProfiles()
            throws Exception {
        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var first = camera(auth, sourceId, "first-pair");
        var second = camera(auth, sourceId, "second-pair");
        var third = camera(auth, sourceId, "third-pair");
        var fourth = camera(auth, sourceId, "fourth-pair");
        var neverChecked = camera(auth, sourceId, "never-pair");
        addFact(first, 0, "ACCESSIBLE", false);
        addFact(first, 1, "FAILED", true);
        addFact(second, 0, "FAILED", false);
        addFact(second, 1, "FAILED", false);
        addFact(third, 0, "ACCESSIBLE", false);
        addFact(fourth, 0, "FAILED", true);
        assertThat(
                        read(
                                        "/camera-status?layer=CONTROL&name=pair&size=1&page=1&observation=FAILED",
                                        auth)
                                .path("total")
                                .asInt())
                .isEqualTo(2);
        var page2 =
                read(
                        "/camera-status?layer=CONTROL&name=pair&size=1&page=2&observation=FAILED",
                        auth);
        assertThat(page2.path("total").asInt()).isEqualTo(2);
        assertThat(page2.path("items")).hasSize(1);
        assertThat(page2.path("page").asInt()).isEqualTo(2);
        assertThat(page2.path("items").get(0).path("cameraId")).isEqualTo(second.path("cameraId"));
        var never = read("/camera-status?layer=CONTROL&observation=NEVER", auth);
        assertThat(never.path("total").asInt()).isEqualTo(1);
        assertThat(never.path("items").get(0).path("cameraId"))
                .isEqualTo(neverChecked.path("cameraId"));
        assertThat(
                        read("/camera-status?layer=CONTROL&observation=FAILED&page=4&size=1", auth)
                                .path("items"))
                .isEmpty();
    }

    @Test
    void pageSharesA2FreshnessConfigurationAndLatestCheckButDoesNotRenewObservations()
            throws Exception {
        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var camera = camera(auth, sourceId, "状态一致");
        enable(auth, camera);
        addFact(camera, 0, "ACCESSIBLE", false);
        addFact(camera, 1, "UNSUPPORTED", true);
        var deviceSummary =
                read("/cameras/devices/page", auth).path("items").get(0).path("statusSummary");
        assertThat(deviceSummary.path("state").asText()).isEqualTo("ACCESSIBLE");
        assertThat(deviceSummary.path("reachableCount").asInt()).isOne();
        assertThat(deviceSummary.path("failedCount").asInt()).isZero();
        addCheck(camera, 0, 101L, LocalDateTime.now().minusSeconds(3));
        addCheck(camera, 0, 102L, LocalDateTime.now().minusSeconds(2));
        var first =
                read("/camera-status?layer=CONTROL", auth).path("items").get(0).path("profiles");
        assertThat(first.get(0).path("control").path("freshness").asText()).isEqualTo("CURRENT");
        assertThat(first.get(0).path("control").path("configurationCurrent").asBoolean()).isTrue();
        assertThat(first.get(0).path("control").path("latestCheck").path("checkId").asText())
                .isEqualTo("102");
        assertThat(first.get(1).path("control").path("freshness").asText()).isEqualTo("STALE");
        var a2 =
                read("/cameras/" + camera.path("cameraId").asText() + "/status", auth)
                        .path("profiles");
        assertThat(first.get(0).path("control").path("observation"))
                .isEqualTo(a2.get(0).path("control").path("observation"));
        assertThat(first.get(0).path("control").path("timeline"))
                .isEqualTo(a2.get(0).path("control").path("timeline"));
        assertThat(first.get(0).path("control").path("timeline").path("stateSince"))
                .isEqualTo(first.get(0).path("control").path("observation").path("observedAt"));
        assertThat(first.get(0).path("control").path("latestCheck"))
                .isEqualTo(a2.get(0).path("control").path("latestCheck"));
        assertThat(read("/camera-status?layer=CONTROL", auth).path("items").get(0).path("profiles"))
                .isEqualTo(first);
        jdbc.update(
                "UPDATE camera_source_endpoint SET port=8554 WHERE source_id=? AND purpose='RTSP'",
                Long.valueOf(sourceId));
        var changed =
                read("/camera-status?layer=CONTROL&observation=ACCESSIBLE", auth)
                        .path("items")
                        .get(0)
                        .path("profiles")
                        .get(0);
        assertThat(changed.path("control").path("configurationCurrent").asBoolean()).isFalse();
        assertThat(changed.path("control").path("freshness").asText()).isEqualTo("STALE");
        assertThat(changed.path("control").path("timeline").isNull()).isTrue();
        assertThat(
                        read("/cameras/devices/page", auth)
                                .path("items")
                                .get(0)
                                .path("statusSummary")
                                .path("state")
                                .asText())
                .isEqualTo("STALE");
        assertThat(changed.path("control").path("observation"))
                .isEqualTo(first.get(0).path("control").path("observation"));
        verifyNoInteractions(worker);
    }

    @Test
    void disabledProfileAndMissingKeyRemainVisibleButCannotCheckAndNoSecretsLeak()
            throws Exception {
        var auth = admin();
        var camera = camera(auth, source(auth).path("sourceId").asText(), "禁用状态");
        enable(auth, camera);
        addFact(camera, 0, "ACCESSIBLE", false);
        long profileId = camera.path("profiles").get(0).path("streamProfileId").asLong();
        jdbc.update("UPDATE camera_stream_profile SET enabled=FALSE WHERE id=?", profileId);
        var page = read("/camera-status?layer=CONTROL", auth);
        var first = page.path("items").get(0).path("profiles").get(0);
        assertThat(first.path("enabled").asBoolean()).isFalse();
        assertThat(first.path("control").path("canCheck").asBoolean()).isFalse();
        assertThat(first.path("control").path("blockingReasonCode").asText())
                .isEqualTo("PROFILE_DISABLED");
        assertThat(first.path("control").path("configurationCurrent").asBoolean()).isFalse();
        assertThat(page.toString())
                .doesNotContain(
                        "/private-main",
                        "/private-sub",
                        "rtsp://",
                        "connectionDigest",
                        "connectionKeyId",
                        "secretCiphertext",
                        "sessionDigest");
        jdbc.update(
                "UPDATE camera_status_snapshot SET connection_key_id='removed-key' WHERE profile_id=?",
                profileId);
        assertThat(
                        read("/camera-status?layer=CONTROL", auth)
                                .path("items")
                                .get(0)
                                .path("profiles")
                                .get(0)
                                .path("control")
                                .path("freshness")
                                .asText())
                .isEqualTo("STALE");
    }

    @Test
    void ordinaryUserScopeRestrictsCountItemsFactsAndChecksAndRevocationRemovesThem()
            throws Exception {
        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var visible = camera(auth, sourceId, "visible-only");
        String hiddenSourceId = source(auth).path("sourceId").asText();
        jdbc.update(
                "UPDATE camera_source_endpoint SET host='10.0.1.66' WHERE source_id=?",
                Long.valueOf(hiddenSourceId));
        var hidden = camera(auth, hiddenSourceId, "hidden-only");
        jdbc.update(
                "UPDATE camera_profile_locator SET rtsp_host_mode='EXPLICIT',rtsp_host='10.0.1.77',rtsp_port=8554 WHERE profile_id=?",
                visible.path("profiles").get(0).path("streamProfileId").asLong());
        enable(auth, visible);
        enable(auth, hidden);
        addFact(visible, 0, "FAILED", false);
        addFact(hidden, 0, "ACCESSIBLE", false);
        addCheck(hidden, 0, 103L, LocalDateTime.now());
        String userId =
                write(post("/user"), auth, Map.of("username", "StatusListUser"), 201)
                        .path("id")
                        .asText();
        jdbc.update(
                "UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?",
                Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role(id,code,name,status,version) VALUES(8921,'STATUS_LIST','状态页','ENABLED',0)");
        jdbc.update(
                "INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8921)", Long.valueOf(userId));
        jdbc.update(
                "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8921,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        write(
                put("/camera-scopes/users/" + userId),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupIds",
                        List.of(),
                        "cameraIds",
                        List.of(visible.path("cameraId").asText())),
                200);
        var ordinary = login("StatusListUser", "Initial1!");
        var page = read("/camera-status?layer=CONTROL&size=1", ordinary);
        var address = page.path("items").get(0).path("accessAddress");
        assertThat(address.path("host").asText()).isEqualTo("10.0.1.5");
        assertThat(address.path("port").asInt()).isEqualTo(554);
        var deviceAddress =
                read("/cameras/devices/page", ordinary).path("items").get(0).path("accessAddress");
        assertThat(deviceAddress).isEqualTo(address);
        var detail = read("/cameras/" + visible.path("cameraId").asText(), ordinary);
        assertThat(detail.path("accessAddress")).isEqualTo(address);
        var streamAddress = detail.path("profiles").get(0).path("locatorSummary");
        assertThat(streamAddress.path("host").asText()).isEqualTo("10.0.1.77");
        assertThat(streamAddress.path("port").asInt()).isEqualTo(8554);
        assertThat(streamAddress.has("pathConfigured")).isFalse();
        assertThat(streamAddress.has("queryConfigured")).isFalse();
        assertThat(detail.has("sourceId")).isFalse();
        assertThat(detail.toString())
                .doesNotContain("/private-main", "/private-sub", "ciphertext", "10.0.1.66");
        assertThat(read("/cameras/devices/page", ordinary).toString()).doesNotContain("10.0.1.66");
        mvc.perform(get("/cameras/" + hidden.path("cameraId").asText()).session(ordinary.session()))
                .andExpect(status().isNotFound());
        assertThat(page.path("total").asInt()).isEqualTo(1);
        assertThat(page.path("items").get(0).path("cameraId")).isEqualTo(visible.path("cameraId"));
        assertThat(page.toString())
                .doesNotContain(
                        hidden.path("cameraId").asText(),
                        "hidden-only",
                        "10.0.1.66",
                        "\"checkId\":\"103\"");
        assertThat(
                        read("/camera-status?layer=CONTROL&observation=ACCESSIBLE", ordinary)
                                .path("total")
                                .asInt())
                .isZero();
        assertThat(read("/camera-status?layer=CONTROL&name=hidden-only", ordinary).path("items"))
                .isEmpty();
        write(
                put("/camera-scopes/users/" + userId),
                auth,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of()),
                200);
        assertThat(read("/camera-status?layer=CONTROL", ordinary).path("total").asInt()).isZero();
        jdbc.update("DELETE FROM sys_role_menu WHERE role_id=8921");
        mvc.perform(get("/camera-status?layer=CONTROL").session(ordinary.session()))
                .andExpect(status().isForbidden());
    }

    @Test
    void deviceAndChannelNamesUseLiteralKeywordFilteringAndPaginationIsBounded() throws Exception {
        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var a = camera(auth, sourceId, "cam-A");
        var b = camera(auth, sourceId, "cam-100%_B");
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,device_type,local_name,source_name) VALUES(9001,?,'device-test','UNKNOWN','门口设备','upstream-device')",
                Long.valueOf(sourceId));
        jdbc.update(
                "UPDATE camera_channel SET device_id=9001 WHERE id=?", a.path("cameraId").asLong());
        var named = read("/camera-status?layer=CONTROL&name=门口设备", auth);
        assertThat(named.path("total").asInt()).isEqualTo(1);
        assertThat(named.path("items").get(0).path("deviceName").asText()).isEqualTo("门口设备");
        assertThat(named.path("items").get(0).path("groupKey").asText()).isEqualTo("d9001");
        assertThat(
                        read("/camera-status?layer=CONTROL&name=upstream-device", auth)
                                .path("total")
                                .asInt())
                .isEqualTo(1);
        var literal =
                mvc.perform(
                                get("/camera-status?layer=CONTROL")
                                        .param("name", "%_")
                                        .session(auth.session()))
                        .andExpect(status().isOk())
                        .andReturn();
        var result = json.readTree(literal.getResponse().getContentAsByteArray()).path("data");
        assertThat(result.path("items").get(0).path("cameraId")).isEqualTo(b.path("cameraId"));
        for (String query :
                List.of(
                        "page=0",
                        "size=0",
                        "size=101",
                        "observation=CURRENT",
                        "observation=OFFLINE"))
            mvc.perform(get("/camera-status?layer=CONTROL&" + query).session(auth.session()))
                    .andExpect(status().isBadRequest());
    }

    @Test
    void profileFanoutOverBudgetFailsClosedBeforeFactsOrTargetResolution() throws Exception {
        var auth = admin();
        String sourceId = source(auth).path("sourceId").asText();
        var camera = camera(auth, sourceId, "profile-budget");
        var overBudget =
                java.util.stream.IntStream.range(0, 3201)
                        .mapToObj(
                                index -> {
                                    var profile =
                                            new com.streamfusion.platform.camera.pojo.entity
                                                    .CameraProfileEntity();
                                    profile.setId(10_000L + index);
                                    profile.setChannelId(camera.path("cameraId").asLong());
                                    profile.setSourceId(Long.valueOf(sourceId));
                                    profile.setLabel("fixture-" + index);
                                    profile.setUsageHint("UNKNOWN");
                                    profile.setEnabled(true);
                                    return profile;
                                })
                        .toList();
        doReturn(overBudget).when(profiles).statusProfiles(anyList(), eq(3201));
        clearInvocations(snapshots, checks, targets);
        mvc.perform(get("/camera-status?layer=CONTROL").session(auth.session()))
                .andExpect(status().isConflict());
        verify(profiles, times(1)).statusProfiles(anyList(), eq(3201));
        doReturn(overBudget.subList(0, 1001)).when(profiles).statusProfiles(anyList(), eq(3201));
        mvc.perform(get("/camera-status?layer=CONTROL").session(auth.session()))
                .andExpect(status().isConflict());
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM camera_status_snapshot", Integer.class))
                .isZero();
        verifyNoInteractions(snapshots, checks, targets);
        verifyNoInteractions(worker);
    }

    private void enable(Csrf auth, JsonNode camera) throws Exception {
        String id = camera.path("cameraId").asText();
        String group =
                write(post("/camera-groups"), auth, Map.of("name", "状态组 " + id), 201)
                        .path("groupId")
                        .asText();
        var preview =
                write(
                        post("/cameras/" + id + "/move-preview"),
                        auth,
                        Map.of("cameraVersion", "0", "targetGroupId", group),
                        200);
        write(
                put("/cameras/" + id),
                auth,
                Map.of(
                        "version",
                        "0",
                        "groupId",
                        group,
                        "lifecycle",
                        "ENABLED",
                        "confirmation",
                        preview.path("confirmation").asText()),
                200);
    }

    private void addFact(JsonNode camera, int index, String value, boolean stale) {
        long cameraId = camera.path("cameraId").asLong();
        long profileId = camera.path("profiles").get(index).path("streamProfileId").asLong();
        var inspection =
                targets.inspect(
                        channels.selectById(cameraId),
                        profiles.selectById(profileId),
                        "testkey",
                        () -> {});
        var now = LocalDateTime.now(ZoneId.of("Asia/Shanghai")).withNano(0);
        var fact = new CameraStatusSnapshotEntity();
        fact.setProfileId(profileId);
        fact.setCameraId(cameraId);
        fact.setLayer("CONTROL");
        fact.setProbeKind("RTSP_DESCRIBE");
        fact.setValue(value);
        fact.setReasonCode("RTSP_" + value);
        fact.setObservedAt(now.minusSeconds(3));
        fact.setReadAt(now.minusSeconds(3));
        fact.setValidUntil(stale ? now.minusSeconds(1) : now.plusMinutes(5));
        fact.setCheckRequestedAt(now.minusSeconds(4));
        fact.setCheckId(1L);
        fact.setConnectionKeyId("testkey");
        fact.setConnectionDigest(inspection.connectionDigest());
        fact.setUpdatedAt(now.minusSeconds(3));
        snapshots.insert(fact);
    }

    private void addCheck(JsonNode camera, int index, long id, LocalDateTime requested) {
        var check = new CameraStatusCheckEntity();
        check.setId(id);
        check.setActorUserId(
                jdbc.queryForObject(
                        "SELECT id FROM sys_user WHERE username='CameraAdmin'", Long.class));
        check.setCameraId(camera.path("cameraId").asLong());
        check.setSourceId(camera.path("sourceId").asLong());
        check.setProfileId(camera.path("profiles").get(index).path("streamProfileId").asLong());
        check.setClientRequestId(requestId());
        check.setRequestKeyId("testkey");
        check.setSessionKeyId("testkey");
        check.setConnectionKeyId("testkey");
        check.setRequestFingerprint(new byte[32]);
        check.setSessionDigest(new byte[32]);
        check.setConnectionDigest(new byte[32]);
        check.setLayer("CONTROL");
        check.setProbeKind("RTSP_DESCRIBE");
        check.setExecutionState("SKIPPED");
        check.setReasonCode("WORKER_BUSY");
        check.setTraceId("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
        check.setRequestedAt(requested);
        check.setFinishedAt(requested);
        check.setDeadlineAt(requested.plusSeconds(30));
        check.setUpdatedAt(requested);
        check.setRetainUntil(requested.plusDays(7));
        check.setVersion(0L);
        check.setSnapshotApplied(false);
        checks.insert(check);
    }
}
