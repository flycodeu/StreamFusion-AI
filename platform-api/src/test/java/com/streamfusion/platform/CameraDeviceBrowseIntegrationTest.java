package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.service.CameraAccessService.Visibility;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Autowired;

class CameraDeviceBrowseIntegrationTest extends CameraTestSupport {
    @Autowired CameraChannelMapper channels;

    @Test
    void paginatesDevicesWithoutSplittingChannelsAndKeepsUnidentifiedRecordsSeparate()
            throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String a = camera(auth, source, "A通道1").path("cameraId").asText();
        String b = camera(auth, source, "A通道2").path("cameraId").asText();
        String c = camera(auth, source, "B通道1").path("cameraId").asText();
        String orphan = camera(auth, source, "尚未识别").path("cameraId").asText();
        device(source, 701, "设备A");
        device(source, 702, "设备B");
        jdbc.update("UPDATE camera_channel SET device_id=701 WHERE id IN (?,?)", a, b);
        jdbc.update("UPDATE camera_channel SET device_id=702 WHERE id=?", c);
        var page = read("/cameras/devices/page?size=1", auth);
        assertThat(page.path("total").asInt()).isEqualTo(3);
        assertThat(page.path("items")).hasSize(1);
        var children = read("/cameras/devices/d701/channels?size=1", auth);
        assertThat(children.path("total").asInt()).isEqualTo(2);
        assertThat(children.path("items")).hasSize(1);
        assertThat(
                        read("/cameras/devices/d701/channels?size=1&page=2", auth)
                                .path("items")
                                .get(0)
                                .path("cameraId")
                                .asText())
                .isEqualTo(b);
        assertThat(
                        read("/cameras/devices/page?name=设备A", auth)
                                .path("items")
                                .get(0)
                                .path("channelCount")
                                .asInt())
                .isEqualTo(2);
        assertThat(read("/cameras/devices/c" + orphan + "/channels", auth).path("total").asInt())
                .isOne();
        assertThat(read("/cameras/devices/d999/channels", auth).path("total").asInt()).isZero();

        long allowedGroup =
                Long.parseLong(
                        write(post("/camera-groups"), auth, Map.of("name", "授权区域"), 201)
                                .path("groupId")
                                .asText());
        long otherGroup =
                Long.parseLong(
                        write(post("/camera-groups"), auth, Map.of("name", "其他区域"), 201)
                                .path("groupId")
                                .asText());
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                allowedGroup,
                a);
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle='ENABLED' WHERE id=?",
                otherGroup,
                b);
        var filter = new CameraChannelMapper.Filter(null, null, null, null, null);
        var visibility = new Visibility(false, 999, List.of(allowedGroup));
        var visible = channels.pageDeviceGroups(new Page<>(1, 20), filter, visibility);
        assertThat(visible.getTotal()).isOne();
        assertThat(visible.getRecords().getFirst().getChannelCount()).isOne();
        assertThat(
                        channels.pageDeviceChannels(new Page<>(1, 20), "d701", filter, visibility)
                                .getRecords())
                .extracting(row -> row.getId().toString())
                .containsExactly(a);
        assertThat(
                        channels.pageDeviceChannels(new Page<>(1, 20), "d702", filter, visibility)
                                .getTotal())
                .isZero();
    }

    @Test
    void derivesLegacyStreamRolesWithoutRewritingIdentityOrOverridingManualChoices()
            throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String channel = camera(auth, source, "通道").path("cameraId").asText();
        jdbc.update(
                "UPDATE camera_stream_profile SET usage_hint='UNKNOWN',usage_origin='UNKNOWN',source_label='MediaProfile_Channel2_MainStream' WHERE channel_id=?",
                channel);
        var profile = read("/cameras/" + channel, auth).path("profiles").get(0);
        assertThat(profile.path("usageHint").asText()).isEqualTo("UNKNOWN");
        assertThat(profile.path("classification").path("usageHint").asText()).isEqualTo("MAIN");
        assertThat(profile.path("classification").path("origin").asText()).isEqualTo("NAME_RULE");
        String id = profile.path("streamProfileId").asText();
        var updated =
                write(
                        put("/cameras/" + channel + "/profiles/" + id),
                        auth,
                        Map.of("version", "0", "usageHint", "SUB"),
                        200);
        assertThat(updated.path("classification").path("usageHint").asText()).isEqualTo("SUB");
        assertThat(updated.path("classification").path("origin").asText()).isEqualTo("MANUAL");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM camera_channel", Integer.class))
                .isOne();
    }

    private void device(String source, long id, String name) {
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,source_name,device_type,version,created_at,updated_at) VALUES(?,?,?,?, 'IPC',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                id,
                Long.valueOf(source),
                "device-" + id,
                name);
    }
}
