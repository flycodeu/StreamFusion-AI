package com.streamfusion.platform.camera.access.adapter;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.net.URI;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class CameraLocatorConfigurationTest {
    private final CameraHttpTransport transport = mock(CameraHttpTransport.class);
    private final CameraAccessAdapterRegistry registry =
            new CameraAccessAdapterRegistry(
                    List.of(
                            new HikvisionCameraAccessAdapter(transport),
                            new DahuaCameraAccessAdapter(transport),
                            new OnvifCameraAccessAdapter(transport),
                            new HikPlatformCameraAccessAdapter(
                                    transport, new ObjectMapper(), mock(HikPlatformApi.class))));

    @Test
    void existingProtocolsRetainTheExactPersistentFieldNamesAndOpaqueIdentity() {
        assertThat(
                        registry.locatorConfiguration(
                                new CameraAccessCatalog.Locator(
                                        "HIKVISION", null, "AbC-101 ", null)))
                .isEqualTo(Map.of("profileToken", "AbC-101 "));
        assertThat(
                        registry.locatorConfiguration(
                                new CameraAccessCatalog.Locator("DAHUA", null, "channel-3", 1)))
                .isEqualTo(Map.of("resourceToken", "channel-3", "streamType", 1));
        assertThat(
                        registry.locatorConfiguration(
                                new CameraAccessCatalog.Locator(
                                        "HIK_PLATFORM", null, "camera-A", 0)))
                .isEqualTo(Map.of("resourceToken", "camera-A", "streamType", 0));
        assertThat(
                        registry.locatorConfiguration(
                                new CameraAccessCatalog.Locator(
                                        "ONVIF",
                                        null,
                                        "Profile-A",
                                        null,
                                        "http://www.onvif.org/ver20/media/wsdl",
                                        URI.create("http://192.0.2.10/media2"))))
                .isEqualTo(
                        Map.of(
                                "profileToken",
                                "Profile-A",
                                "serviceNamespace",
                                "http://www.onvif.org/ver20/media/wsdl",
                                "serviceEndpoint",
                                "http://192.0.2.10/media2"));
        verifyNoInteractions(transport);
    }

    @Test
    void malformedProtocolIdentityIsRejectedBeforeAnyPersistenceOrNetwork() {
        assertThatThrownBy(
                        () ->
                                registry.locatorConfiguration(
                                        new CameraAccessCatalog.Locator(
                                                "HIKVISION", null, "bad\nidentity", null)))
                .isInstanceOf(RuntimeException.class);
        assertThatThrownBy(
                        () ->
                                registry.locatorConfiguration(
                                        new CameraAccessCatalog.Locator("DAHUA", null, null, 256)))
                .isInstanceOf(RuntimeException.class);
        assertThatThrownBy(
                        () ->
                                registry.locatorConfiguration(
                                        new CameraAccessCatalog.Locator(
                                                "ONVIF",
                                                null,
                                                "Profile-A",
                                                null,
                                                "unsupported",
                                                URI.create("http://192.0.2.10/media"))))
                .isInstanceOf(RuntimeException.class);
        verifyNoInteractions(transport);
    }

    @Test
    void platformAndDeviceAuthenticationRequirementsStayDistinct() {
        registry.validateCredentials("HIKVISION", null, null);
        registry.validateCredentials("HIK_PLATFORM", "app-key", "app-secret");
        assertThatThrownBy(() -> registry.validateCredentials("HIK_PLATFORM", null, null))
                .isInstanceOf(RuntimeException.class);
        assertThatThrownBy(() -> registry.validateCredentials("HIKVISION", "user", null))
                .isInstanceOf(RuntimeException.class);
        assertThat(registry.platform("HIK_PLATFORM").descriptor().inputKind())
                .isEqualTo("PLATFORM_APPKEY");
        assertThatThrownBy(() -> registry.platform("HIKVISION"))
                .hasMessage("PROTOCOL_NOT_SUPPORTED");
    }

    @Test
    void hikPlatformRootKeepsPageOneRuleAndSafeNormalizedRegionShape() {
        var api = mock(HikPlatformApi.class);
        var adapter = new HikPlatformCameraAccessAdapter(transport, new ObjectMapper(), api);
        var context =
                new CameraAccessContext(
                        URI.create("https://192.0.2.10/artemis"),
                        "fixture",
                        "key",
                        "secret",
                        554,
                        () -> {});
        when(api.root(context))
                .thenReturn(
                        new ObjectMapper()
                                .createObjectNode()
                                .put("indexCode", "root-A")
                                .put("name", "Root"));
        assertThat(adapter.regions(context, null, 1, 25).items())
                .containsExactly(new CameraPlatformAccessAdapter.Region("root-A", null, "Root"));
        assertThatThrownBy(() -> adapter.regions(context, null, 2, 25))
                .isInstanceOf(com.streamfusion.platform.common.exception.BusinessException.class);
        verify(api).root(context);
        verifyNoMoreInteractions(api);
    }
}
