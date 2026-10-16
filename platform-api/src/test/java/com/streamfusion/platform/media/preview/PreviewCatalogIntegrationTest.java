package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.assertThat;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.jsonPath;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import com.streamfusion.platform.support.CameraTestSupport;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

/**
 * Exercises the preview HTTP boundary and its real SQL visibility rules without contacting media.
 */
class PreviewCatalogIntegrationTest extends CameraTestSupport {
    @Test
    void paginatesAndSearchesMetadataWithoutExposingSourceLocators() throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String first = camera(auth, source, "厂区北门").path("cameraId").asText();
        String middle = camera(auth, source, "车间通道").path("cameraId").asText();
        String last = camera(auth, source, "厂区南门").path("cameraId").asText();

        var page = read("/preview/catalog?size=1", auth);
        assertThat(page.path("page").asInt()).isOne();
        assertThat(page.path("size").asInt()).isOne();
        assertThat(page.path("total").asInt()).isEqualTo(3);
        assertThat(page.path("items")).hasSize(1);
        var channel = page.path("items").get(0);
        assertThat(channel.path("cameraId").asText()).isEqualTo(last);
        assertThat(channel.path("deviceGroupKey").asText()).isEqualTo("c" + last);
        assertThat(channel.path("deviceName").asText()).isEqualTo("厂区南门");
        assertThat(channel.path("profiles")).hasSize(2);
        assertThat(channel.path("profiles").get(0).path("usage").asText()).isEqualTo("MAIN");
        assertThat(channel.path("profiles").get(1).path("usage").asText()).isEqualTo("SUB");
        for (var profile : channel.path("profiles")) {
            assertThat(profile.path("enabled").asBoolean()).isTrue();
            assertThat(profile.path("configured").asBoolean()).isTrue();
            assertThat(profile.has("locator")).isFalse();
            assertThat(profile.has("sourceId")).isFalse();
        }
        assertThat(page.toString())
                .doesNotContain("/private-main", "/private-sub", "10.0.1.5", "Ciphertext");
        assertThat(
                        read("/preview/catalog?size=1&page=2", auth)
                                .path("items")
                                .get(0)
                                .path("cameraId")
                                .asText())
                .isEqualTo(middle);
        var filtered = read("/preview/catalog?name=厂区&size=1&page=2", auth);
        assertThat(filtered.path("total").asInt()).isEqualTo(2);
        assertThat(filtered.path("items").get(0).path("cameraId").asText()).isEqualTo(first);
        assertThat(read("/preview/catalog?size=1&page=4", auth).path("items")).isEmpty();
    }

    @Test
    void deviceFilterReturnsItsChannelsAndKeepsUnidentifiedCamerasSeparate() throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String first = camera(auth, source, "双目通道1").path("cameraId").asText();
        String second = camera(auth, source, "双目通道2").path("cameraId").asText();
        String other = camera(auth, source, "其他设备通道").path("cameraId").asText();
        String orphan = camera(auth, source, "独立相机").path("cameraId").asText();
        device(source, 701, "双目相机", "双目相机本地名称");
        device(source, 702, "其他设备", null);
        jdbc.update("UPDATE camera_channel SET device_id=701 WHERE id IN (?,?)", first, second);
        jdbc.update("UPDATE camera_channel SET device_id=702 WHERE id=?", other);

        var page = read("/preview/catalog?device=d701&size=1", auth);
        assertThat(page.path("total").asInt()).isEqualTo(2);
        assertThat(page.path("items")).hasSize(1);
        var channel = page.path("items").get(0);
        assertThat(channel.path("cameraId").asText()).isEqualTo(first);
        assertThat(channel.path("deviceGroupKey").asText()).isEqualTo("d701");
        assertThat(channel.path("deviceName").asText()).isEqualTo("双目相机本地名称");
        assertThat(channel.path("profiles")).hasSize(2);
        assertThat(
                        read("/preview/catalog?device=d701&size=1&page=2", auth)
                                .path("items")
                                .get(0)
                                .path("cameraId")
                                .asText())
                .isEqualTo(second);
        assertThat(
                        read("/preview/catalog?device=c" + orphan, auth)
                                .path("items")
                                .get(0)
                                .path("cameraId")
                                .asText())
                .isEqualTo(orphan);
        assertThat(read("/preview/catalog?device=c" + first, auth).path("total").asInt()).isZero();
        assertThat(read("/preview/catalog?device=d999", auth).path("total").asInt()).isZero();
    }

    @Test
    void ordinaryScopeFiltersDeviceChannelsAndRefreshesWithinAnExistingSession() throws Exception {
        var auth = admin();
        String source = source(auth).path("sourceId").asText();
        String direct = camera(auth, source, "直接授权通道").path("cameraId").asText();
        String hidden = camera(auth, source, "同设备隐藏通道").path("cameraId").asText();
        String grouped = camera(auth, source, "子区域授权通道").path("cameraId").asText();
        String disabled = camera(auth, source, "已停用通道").path("cameraId").asText();
        String root = group(auth, "授权区域", null);
        String child = group(auth, "授权子区域", root);
        String other = group(auth, "其他区域", null);
        device(source, 701, "双通道设备", null);
        jdbc.update("UPDATE camera_channel SET device_id=701 WHERE id IN (?,?)", direct, hidden);
        assign(direct, other, "ENABLED");
        assign(hidden, other, "ENABLED");
        assign(grouped, child, "ENABLED");
        assign(disabled, child, "DISABLED");
        String user = ordinaryUser(auth, "PreviewWorker", true);
        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "0", "groupIds", List.of(root), "cameraIds", List.of(direct)),
                200);
        var worker = login("PreviewWorker", "Initial1!");

        var catalog = read("/preview/catalog", worker);
        assertThat(catalog.path("total").asInt()).isEqualTo(3);
        assertThat(catalog.path("items"))
                .extracting(row -> row.path("cameraId").asText())
                .containsExactlyInAnyOrder(direct, grouped, disabled);
        assertThat(catalog.toString()).doesNotContain("同设备隐藏通道");
        assertThat(catalog.path("items"))
                .anyMatch(
                        row ->
                                row.path("cameraId").asText().equals(disabled)
                                        && row.path("lifecycle").asText().equals("DISABLED"));
        var device = read("/preview/catalog?device=d701", worker);
        assertThat(device.path("total").asInt()).isOne();
        assertThat(device.path("items").get(0).path("cameraId").asText()).isEqualTo(direct);
        assertThat(read("/preview/catalog?name=隐藏", worker).path("total").asInt()).isZero();
        assertThat(
                        read("/preview/catalog?device=c" + disabled, worker)
                                .path("items")
                                .get(0)
                                .path("lifecycle")
                                .asText())
                .isEqualTo("DISABLED");

        write(
                put("/camera-scopes/users/" + user),
                auth,
                Map.of("version", "1", "groupIds", List.of(), "cameraIds", List.of()),
                200);
        assertThat(read("/preview/catalog", worker).path("total").asInt()).isZero();
        assertThat(read("/preview/catalog?device=d701", worker).path("items")).isEmpty();
    }

    @Test
    void rejectsMissingPagePermissionAndMalformedDeviceFilters() throws Exception {
        mvc.perform(get("/preview/catalog")).andExpect(status().isUnauthorized());
        var auth = admin();
        ordinaryUser(auth, "PreviewNoPage", false);
        var noPage = login("PreviewNoPage", "Initial1!");
        mvc.perform(get("/preview/catalog").session(noPage.session()))
                .andExpect(status().isForbidden());
        for (String device : List.of("701", "d0", "c-1", "d9223372036854775808")) {
            mvc.perform(get("/preview/catalog").param("device", device).session(auth.session()))
                    .andExpect(status().isBadRequest())
                    .andExpect(jsonPath("$.code").value("VALIDATION_ERROR"));
        }
    }

    private String ordinaryUser(Csrf auth, String username, boolean cameraPage) throws Exception {
        String user =
                write(post("/user"), auth, Map.of("username", username), 201).path("id").asText();
        jdbc.update("UPDATE sys_user SET status=1,must_change_password=FALSE WHERE id=?", user);
        if (cameraPage) {
            jdbc.update(
                    "INSERT INTO sys_role(id,code,name,status,version) VALUES(8901,'PREVIEW_TEST','预览测试','ENABLED',0)");
            jdbc.update("INSERT INTO sys_user_role(user_id,role_id) VALUES(?,8901)", user);
            jdbc.update(
                    "INSERT INTO sys_role_menu(role_id,menu_id) SELECT 8901,id FROM sys_menu WHERE type='PAGE' AND module_key='camera'");
        }
        return user;
    }

    private String group(Csrf auth, String name, String parent) throws Exception {
        Map<String, Object> body =
                parent == null ? Map.of("name", name) : Map.of("name", name, "parentId", parent);
        return write(post("/camera-groups"), auth, body, 201).path("groupId").asText();
    }

    private void assign(String channel, String group, String lifecycle) {
        jdbc.update(
                "UPDATE camera_channel SET group_id=?,lifecycle=? WHERE id=?",
                group,
                lifecycle,
                channel);
    }

    private void device(String source, long id, String name, String localName) {
        jdbc.update(
                "INSERT INTO camera_device(id,source_id,external_device_key,source_name,local_name,device_type,version,created_at,updated_at) VALUES(?,?,?,?,?,'IPC',0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                id,
                Long.valueOf(source),
                "device-" + id,
                name,
                localName);
    }
}
