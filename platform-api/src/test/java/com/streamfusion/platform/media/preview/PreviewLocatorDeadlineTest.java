package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.mapper.CameraCredentialMapper;
import com.streamfusion.platform.camera.mapper.CameraEndpointMapper;
import com.streamfusion.platform.camera.mapper.CameraLocatorMapper;
import com.streamfusion.platform.camera.mapper.CameraSourceMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraEndpointEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraSourceEntity;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.media.preview.source.PreviewLocatorService;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapterRegistry;
import java.net.URI;
import java.time.Duration;
import java.util.List;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class PreviewLocatorDeadlineTest {
    final CameraLocatorMapper locators = mock(CameraLocatorMapper.class);
    final CameraSourceMapper sources = mock(CameraSourceMapper.class);
    final CameraEndpointMapper endpoints = mock(CameraEndpointMapper.class);
    final CameraCredentialMapper credentials = mock(CameraCredentialMapper.class);
    final CameraCryptoService crypto = mock(CameraCryptoService.class);
    final CameraSourceRules rules = mock(CameraSourceRules.class);
    final StreamLocatorAdapter adapter = mock(StreamLocatorAdapter.class);
    final Runnable authorization = mock(Runnable.class);
    final CameraProfileEntity profile = new CameraProfileEntity();
    PreviewLocatorService service;

    @BeforeEach
    void setup() {
        profile.setId(20L);
        profile.setSourceId(30L);
        var locator = new CameraLocatorEntity();
        locator.setProfileId(20L);
        locator.setSourceId(30L);
        locator.setLocatorKind("RTSP");
        var source = new CameraSourceEntity();
        source.setId(30L);
        source.setEnabled(true);
        source.setNetworkPolicyKey("LAN");
        var endpoint = new CameraEndpointEntity();
        endpoint.setHost("127.0.0.1");
        endpoint.setPort(554);
        endpoint.setScheme("rtsp");
        when(locators.selectById(20L)).thenReturn(locator);
        when(sources.selectById(30L)).thenReturn(source);
        when(endpoints.selectOne(any())).thenReturn(endpoint);
        when(crypto.decrypt(anyString(), any())).thenReturn(new ObjectMapper().createObjectNode());
        when(adapter.type()).thenReturn("RTSP");
        when(adapter.resolve(any(), any(), any())).thenReturn(URI.create("rtsp://127.0.0.1/live"));
        service =
                new PreviewLocatorService(
                        locators,
                        sources,
                        endpoints,
                        credentials,
                        crypto,
                        rules,
                        new StreamLocatorAdapterRegistry(List.of(adapter)));
    }

    @AfterEach
    void close() {
        service.close();
    }

    @Test
    void platformSigningCredentialsAreNeverAttachedToTheReturnedRtspAddress() {
        var locator = locators.selectById(20L);
        locator.setLocatorKind("HIK_PLATFORM");
        var endpoint = endpoints.selectOne(null);
        endpoint.setScheme("https");
        endpoint.setCredentialId(40L);
        var credential = new com.streamfusion.platform.camera.pojo.entity.CameraCredentialEntity();
        credential.setId(40L);
        credential.setSourceId(30L);
        credential.setPurpose("PLATFORM_HTTP");
        when(credentials.selectById(40L)).thenReturn(credential);
        when(crypto.decrypt(startsWith("camera_source_credential/"), any()))
                .thenReturn(
                        new ObjectMapper()
                                .createObjectNode()
                                .put("username", "private-appkey")
                                .put("password", "private-appsecret"));
        when(adapter.type()).thenReturn("HIK_PLATFORM");
        service.close();
        service =
                new PreviewLocatorService(
                        locators,
                        sources,
                        endpoints,
                        credentials,
                        crypto,
                        rules,
                        new StreamLocatorAdapterRegistry(List.of(adapter)));
        var uri = service.resolve(profile, authorization).uri();
        assertThat(uri.getRawUserInfo()).isNull();
        assertThat(uri.toString()).doesNotContain("private-appkey", "private-appsecret");
        verify(adapter)
                .resolve(
                        any(),
                        any(),
                        argThat(
                                c ->
                                        "private-appkey".equals(c.username())
                                                && "private-appsecret".equals(c.password())));
    }

    @Test
    void adapterReceivesRemainingCreationBudgetInsteadOfANewTenSeconds() {
        var deadline = PreviewDeadline.after(Duration.ofSeconds(2));
        when(adapter.resolve(any(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            CameraAccessContext context = invocation.getArgument(2);
                            long remaining = context.deadlineNanos() - System.nanoTime();
                            assertThat(remaining)
                                    .isPositive()
                                    .isLessThan(TimeUnit.SECONDS.toNanos(2));
                            context.checkAuthorization().run();
                            return URI.create("rtsp://127.0.0.1/live");
                        });
        assertThat(service.resolve(profile, authorization, deadline).uri().getHost())
                .isEqualTo("127.0.0.1");
        verify(authorization, times(3)).run();
    }

    @Test
    void cancelledBudgetNeverReadsLocatorsOrStartsNetworkWork() {
        var deadline = PreviewDeadline.after(Duration.ofSeconds(2));
        deadline.cancel();
        assertThatThrownBy(() -> service.resolve(profile, authorization, deadline))
                .hasMessage("CANCELLED");
        verifyNoInteractions(locators, sources, endpoints, credentials, crypto, authorization);
    }

    @Test
    void cancellationAfterAdapterDoesNotProceedToDnsOrReturnAnAddress() {
        var deadline = PreviewDeadline.after(Duration.ofSeconds(2));
        when(adapter.resolve(any(), any(), any()))
                .thenAnswer(
                        invocation -> {
                            deadline.cancel();
                            return URI.create("rtsp://127.0.0.1/live");
                        });
        assertThatThrownBy(() -> service.resolve(profile, authorization, deadline))
                .hasMessage("CANCELLED");
        verifyNoInteractions(rules);
    }
}
