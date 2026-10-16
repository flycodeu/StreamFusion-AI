package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.ArgumentMatchers.anyList;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.mapper.CameraEndpointMapper;
import com.streamfusion.platform.camera.status.CameraStatusWorker;
import com.streamfusion.platform.support.CameraTestSupport;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.springframework.test.context.bean.override.mockito.MockitoBean;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;

/** Address enrichment is bounded to the authorized page, with the original endpoint priority. */
class CameraAddressReadIntegrationTest extends CameraTestSupport {
    @MockitoSpyBean CameraEndpointMapper endpoints;
    @MockitoBean CameraStatusWorker worker;

    @Test
    void allListReadersEnrichOnlyThePageAndEmptyPagesDoNotReadEndpoints() throws Exception {
        var auth = admin();
        long firstSource = source(auth).path("sourceId").asLong();
        var first = camera(auth, Long.toString(firstSource), "first");
        long lastSource = source(auth).path("sourceId").asLong();
        var last = camera(auth, Long.toString(lastSource), "last");
        jdbc.update(
                "UPDATE camera_channel SET created_at='2026-01-01 00:00:00' WHERE id=?",
                first.path("cameraId").asLong());
        jdbc.update(
                "UPDATE camera_channel SET created_at='2026-02-01 00:00:00' WHERE id=?",
                last.path("cameraId").asLong());
        for (String path :
                List.of("/cameras/page", "/cameras/devices/page", "/camera-status?layer=CONTROL")) {
            clearInvocations(endpoints);
            var page = read(path + (path.contains("?") ? "&" : "?") + "size=1", auth);
            assertThat(page.path("total").asInt()).isEqualTo(2);
            assertAddress(page.path("items").get(0), "RTSP", "10.0.1.5", 554);
            verify(endpoints).displayAddresses(List.of(lastSource));
            clearInvocations(endpoints);
            assertThat(
                            read(path + (path.contains("?") ? "&" : "?") + "page=3&size=1", auth)
                                    .path("items"))
                    .isEmpty();
            verify(endpoints, never()).displayAddresses(anyList());
        }
        verifyNoInteractions(worker);
    }

    @Test
    void deviceEndpointPriorityAndMissingAddressDoNotChangeCountsOrLeakSecrets() throws Exception {
        var auth = admin();
        long source = source(auth).path("sourceId").asLong();
        var camera = camera(auth, Long.toString(source), "address-priority");
        jdbc.update(
                "UPDATE camera_source SET connection_category='DEVICE',adapter_type='ONVIF' WHERE id=?",
                source);
        endpoint(901, source, "VENDOR_HTTP", "10.0.1.11");
        endpoint(902, source, "ONVIF", "10.0.1.12");
        assertAddress(
                read("/cameras/" + camera.path("cameraId").asText(), auth),
                "DEVICE",
                "10.0.1.12",
                80);
        endpoint(903, source, "DEVICE_HTTP", "10.0.1.13");
        for (String path :
                List.of("/cameras/page", "/cameras/devices/page", "/camera-status?layer=CONTROL")) {
            var page = read(path, auth);
            assertThat(page.path("total").asInt()).isEqualTo(1);
            assertAddress(page.path("items").get(0), "DEVICE", "10.0.1.13", 80);
            assertThat(page.toString()).doesNotContain("credentialId", "basePath", "authMode");
        }
        // A platform source must not fall back to an unrelated RTSP/device endpoint.
        jdbc.update(
                "UPDATE camera_source SET connection_category='PLATFORM',adapter_type='HIK_PLATFORM' WHERE id=?",
                source);
        var page = read("/cameras/page", auth);
        assertThat(page.path("total").asInt()).isEqualTo(1);
        assertThat(page.path("items").get(0).hasNonNull("accessAddress")).isFalse();
    }

    private void endpoint(long id, long source, String purpose, String host) {
        jdbc.update(
                "INSERT INTO camera_source_endpoint(id,source_id,purpose,scheme,host,port,base_path,auth_mode,tls_policy,created_at,updated_at) VALUES(?,?,?,'http',?,80,?,'NONE','SYSTEM_CA',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)",
                id,
                source,
                purpose,
                host,
                "ONVIF".equals(purpose) ? "/onvif/device_service" : "");
    }

    private static void assertAddress(JsonNode item, String kind, String host, int port) {
        var address = item.path("accessAddress");
        assertThat(address.path("kind").asText()).isEqualTo(kind);
        assertThat(address.path("host").asText()).isEqualTo(host);
        assertThat(address.path("port").asInt()).isEqualTo(port);
    }
}
