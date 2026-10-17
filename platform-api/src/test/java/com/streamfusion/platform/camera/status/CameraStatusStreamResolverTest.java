package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapterRegistry;
import java.net.URI;
import java.util.List;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;

class CameraStatusStreamResolverTest {
    private final ObjectMapper json = new ObjectMapper();
    private final CameraAccessContext context =
            new CameraAccessContext(
                    URI.create("http://192.0.2.10"), "fixture", "user", "secret", 554, () -> {});

    @Test
    void registeredLocatorIsUsedWithoutAnotherBrandDispatchList() {
        var adapter = mock(StreamLocatorAdapter.class);
        when(adapter.type()).thenReturn("FIXTURE");
        var resolver =
                new CameraStatusStreamResolver(new StreamLocatorAdapterRegistry(List.of(adapter)));
        var locator = locator("FIXTURE");
        var config = json.createObjectNode().put("profileToken", "Profile-A");
        URI expected = URI.create("rtsp://192.0.2.10:554/fixture");
        when(adapter.resolve(locator, config, context)).thenReturn(expected);

        assertThat(resolver.supports("FIXTURE")).isTrue();
        assertThat(resolver.supports("UNKNOWN")).isFalse();
        assertThat(resolver.configuredUri(locator, config, context)).isEqualTo(expected);
        verify(adapter).resolve(locator, config, context);
    }

    @ParameterizedTest
    @CsvSource({
        "UPSTREAM_REJECTED,STREAM_URI_UNAVAILABLE",
        "LOCATOR_INVALID,STREAM_URI_INVALID",
        "CANCELLED,CANCELLED"
    })
    void lookupPreservesFailureCategoryAndWorkerBudget(String failure, String expected) {
        var adapter = mock(StreamLocatorAdapter.class);
        when(adapter.type()).thenReturn("ONVIF");
        var resolver =
                new CameraStatusStreamResolver(new StreamLocatorAdapterRegistry(List.of(adapter)));
        var locator = locator("ONVIF");
        var config = json.createObjectNode();
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(3);
        Runnable checkActive = mock(Runnable.class);
        when(adapter.resolve(eq(locator), eq(config), any()))
                .thenAnswer(
                        invocation -> {
                            CameraAccessContext execution = invocation.getArgument(2);
                            assertThat(execution.deadlineNanos()).isEqualTo(deadline);
                            assertThat(execution.checkAuthorization()).isSameAs(checkActive);
                            throw new PreviewFailure(failure);
                        });

        assertThatThrownBy(
                        () ->
                                resolver.deferred(locator, config, context)
                                        .resolve(deadline, checkActive))
                .isInstanceOf(CameraAdapterException.class)
                .hasMessage(expected);
        verify(checkActive).run();
    }

    @Test
    void invalidReturnedUriIsClassifiedAsAddressFailure() {
        var adapter = mock(StreamLocatorAdapter.class);
        when(adapter.type()).thenReturn("ONVIF");
        var resolver =
                new CameraStatusStreamResolver(new StreamLocatorAdapterRegistry(List.of(adapter)));
        when(adapter.resolve(any(), any(), any()))
                .thenReturn(URI.create("http://192.0.2.10/not-rtsp"));
        assertThatThrownBy(
                        () ->
                                resolver.deferred(
                                                locator("ONVIF"), json.createObjectNode(), context)
                                        .resolve(
                                                System.nanoTime() + TimeUnit.SECONDS.toNanos(3),
                                                () -> {}))
                .hasMessage("STREAM_URI_INVALID");
    }

    private static CameraLocatorEntity locator(String kind) {
        var locator = new CameraLocatorEntity();
        locator.setLocatorKind(kind);
        return locator;
    }

    @Test
    void invalidOrDuplicateRegistrationFailsClearlyAndNullLookupIsUnsupported() {
        var adapter = mock(StreamLocatorAdapter.class);
        when(adapter.type()).thenReturn("ONVIF");
        var registry = new StreamLocatorAdapterRegistry(List.of(adapter));
        assertThat(registry.supports(null)).isFalse();
        assertThatThrownBy(() -> registry.required(null))
                .hasMessage("SOURCE_PROTOCOL_NOT_SUPPORTED");
        assertThatThrownBy(() -> new StreamLocatorAdapterRegistry(List.of(adapter, adapter)))
                .isInstanceOf(IllegalStateException.class)
                .hasMessage("Duplicate stream locator adapter");
    }
}
