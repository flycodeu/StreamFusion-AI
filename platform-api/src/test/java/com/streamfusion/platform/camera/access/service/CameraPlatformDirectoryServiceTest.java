package com.streamfusion.platform.camera.access.service;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterDescriptor;
import com.streamfusion.platform.camera.access.adapter.CameraPlatformAccessAdapter;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraSourceService;
import java.net.URI;
import java.util.List;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class CameraPlatformDirectoryServiceTest {
    private final CameraAccessService access = mock(CameraAccessService.class);
    private final CameraSourceService sources = mock(CameraSourceService.class);
    private final CameraConnectionRules connections = mock(CameraConnectionRules.class);
    private final CameraPlatformAccessAdapter adapter = mock(CameraPlatformAccessAdapter.class);
    private final CameraConnection connection =
            new CameraConnection(
                    "FIXTURE_PLATFORM",
                    "fixture",
                    "192.0.2.10",
                    443,
                    "https",
                    "app",
                    "secret",
                    554,
                    "fixture",
                    null,
                    null,
                    null);

    @Test
    void registeredPlatformOwnsRegionMappingWhileServicePreservesAuthorizationAndBudget() {
        var service = service();
        when(access.readActor()).thenReturn(new CameraAccessService.Actor(7L, true));
        when(adapter.regions(any(), eq("region-A"), eq(2), eq(25)))
                .thenAnswer(
                        invocation -> {
                            CameraAccessContext context = invocation.getArgument(0);
                            assertThat(context.deadlineNanos() - System.nanoTime())
                                    .isPositive()
                                    .isLessThanOrEqualTo(TimeUnit.SECONDS.toNanos(10));
                            context.checkAuthorization().run();
                            return new CameraPlatformAccessAdapter.Regions(
                                    List.of(
                                            new CameraPlatformAccessAdapter.Region(
                                                    "child", "region-A", "Area")),
                                    40,
                                    2,
                                    25);
                        });

        var result =
                service.regions(
                        new CameraPlatformDirectoryService.Query(connection, "region-A", 2, 25));
        assertThat(result.items())
                .containsExactly(
                        new CameraPlatformDirectoryService.Region("child", "region-A", "Area"));
        assertThat(result.total()).isEqualTo(40);
        assertThat(result.page()).isEqualTo(2);
        assertThat(result.size()).isEqualTo(25);
        verify(access, times(2)).requireSuper(any());
    }

    @Test
    void authorityChangeDuringPlatformCallIsNotHiddenByAdapterBoundary() {
        var service = service();
        when(access.readActor())
                .thenReturn(
                        new CameraAccessService.Actor(7L, true),
                        new CameraAccessService.Actor(8L, true));
        when(adapter.regions(any(), isNull(), eq(1), eq(100)))
                .thenAnswer(
                        invocation -> {
                            ((CameraAccessContext) invocation.getArgument(0))
                                    .checkAuthorization()
                                    .run();
                            throw new AssertionError(
                                    "revoked actor must stop before external work");
                        });
        assertThatThrownBy(
                        () ->
                                service.regions(
                                        new CameraPlatformDirectoryService.Query(
                                                connection, null, null, null)))
                .hasMessage("ACTOR_REVOKED");
    }

    @Test
    void oversizedPageIsRejectedBeforeInvokingThePlatform() {
        var service = service();
        when(access.readActor()).thenReturn(new CameraAccessService.Actor(7L, true));
        assertThatThrownBy(
                        () ->
                                service.regions(
                                        new CameraPlatformDirectoryService.Query(
                                                connection, "region-A", 1, 101)))
                .isInstanceOf(com.streamfusion.platform.common.exception.BusinessException.class);
        verify(adapter, never()).regions(any(), any(), anyInt(), anyInt());
    }

    private CameraPlatformDirectoryService service() {
        when(adapter.type()).thenReturn("FIXTURE_PLATFORM");
        when(adapter.descriptor())
                .thenReturn(
                        new CameraAdapterDescriptor(
                                "FIXTURE_PLATFORM",
                                "Fixture platform",
                                "PLATFORM",
                                "PLATFORM_APPKEY",
                                false,
                                40,
                                true));
        when(sources.resolveConnection(connection)).thenReturn(connection);
        when(connections.normalize(connection)).thenReturn(connection);
        when(connections.endpoint(connection)).thenReturn(URI.create("https://192.0.2.10/fixture"));
        return new CameraPlatformDirectoryService(
                access, sources, connections, new CameraAccessAdapterRegistry(List.of(adapter)));
    }
}
