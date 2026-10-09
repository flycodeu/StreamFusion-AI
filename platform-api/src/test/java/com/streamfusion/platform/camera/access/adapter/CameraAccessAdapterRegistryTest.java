package com.streamfusion.platform.camera.access.adapter;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.net.URI;
import java.util.List;
import org.junit.jupiter.api.Test;

class CameraAccessAdapterRegistryTest {
    private final CameraAccessContext context =
            new CameraAccessContext(
                    URI.create("http://192.0.2.20"), "fixture", "user", "secret", 554, () -> {});
    private final CameraAccessAdapter hik = adapter("HIKVISION", 10);
    private final CameraAccessAdapter dahua = adapter("DAHUA", 20);
    private final CameraAccessAdapter onvif = adapter("ONVIF", 30);
    private final CameraAccessAdapterRegistry registry =
            new CameraAccessAdapterRegistry(List.of(hik, dahua, onvif));

    @Test
    void vendorAuthenticationFailureFallsBackOnceToOnvifWithoutTryingAnotherVendor() {
        when(hik.discover(context)).thenThrow(new CameraAdapterException("AUTHENTICATION_FAILED"));
        when(onvif.discover(context)).thenReturn(catalog("ONVIF", true));
        var result = registry.discover("AUTO", context);
        assertThat(result.adapterType()).isEqualTo("ONVIF");
        assertThat(result.warnings()).contains("AUTO_NATIVE_AUTH_REJECTED");
        verify(hik).discover(context);
        verify(onvif).discover(context);
        verify(dahua, never()).discover(context);
        assertThatThrownBy(() -> registry.discover("HIKVISION", context))
                .hasMessage("AUTHENTICATION_FAILED");
        verify(onvif).discover(context);
    }

    @Test
    void incompleteNativeCatalogUsesCompleteOnvifWithoutMergingIdentityKeys() {
        when(hik.discover(context)).thenThrow(new CameraAdapterException("PROTOCOL_NOT_SUPPORTED"));
        when(dahua.discover(context)).thenReturn(catalog("DAHUA", false));
        when(onvif.discover(context)).thenReturn(catalog("ONVIF", true));
        var result = registry.discover("AUTO", context);
        assertThat(result.adapterType()).isEqualTo("ONVIF");
        assertThat(result.warnings()).contains("AUTO_NATIVE_CATALOG_PARTIAL");
        when(onvif.discover(context))
                .thenThrow(new CameraAdapterException("AUTHENTICATION_FAILED"));
        assertThat(registry.discover("AUTO", context).adapterType()).isEqualTo("DAHUA");
    }

    @Test
    void bothProtocolsRejectingCredentialsTerminatesWithoutRepeatingAttempts() {
        when(hik.discover(context)).thenThrow(new CameraAdapterException("AUTHENTICATION_FAILED"));
        when(onvif.discover(context))
                .thenThrow(new CameraAdapterException("AUTHENTICATION_FAILED"));
        assertThatThrownBy(() -> registry.discover("AUTO", context))
                .hasMessage("AUTHENTICATION_FAILED");
        verify(hik).discover(context);
        verify(onvif).discover(context);
        verify(dahua, never()).discover(context);
    }

    @Test
    void transportAndAuthorizationFailuresNeverTriggerProtocolFallback() {
        for (String reason :
                List.of(
                        "DESTINATION_NOT_ALLOWED",
                        "TLS_VALIDATION_FAILED",
                        "NETWORK_TIMEOUT",
                        "JOB_NO_LONGER_ACTIVE")) {
            doThrow(new CameraAdapterException(reason)).when(hik).discover(context);
            assertThatThrownBy(() -> registry.discover("AUTO", context)).hasMessage(reason);
        }
        verify(onvif, never()).discover(context);
        verify(dahua, never()).discover(context);
    }

    private static CameraAccessAdapter adapter(String type, int order) {
        var result = mock(CameraAccessAdapter.class);
        when(result.type()).thenReturn(type);
        when(result.descriptor())
                .thenReturn(
                        new CameraAdapterDescriptor(
                                type, type, "DEVICE", "DEVICE_LOGIN", true, order, false));
        return result;
    }

    private static CameraAccessCatalog catalog(String type, boolean complete) {
        return new CameraAccessCatalog(type, null, List.of(), complete, List.of());
    }
}
