package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.mapper.CameraCredentialMapper;
import com.streamfusion.platform.camera.mapper.CameraEndpointMapper;
import com.streamfusion.platform.camera.mapper.CameraLocatorMapper;
import com.streamfusion.platform.camera.mapper.CameraSourceMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraCredentialEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraEndpointEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraSourceEntity;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.media.preview.source.PreviewLocatorService;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapter;
import java.time.LocalDateTime;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Real local HMAC/encryption with mapper fixtures; never resolves or contacts a source. */
class PreviewConnectionKeyTest {
    private final CameraLocatorMapper locators = mock(CameraLocatorMapper.class);
    private final CameraSourceMapper sources = mock(CameraSourceMapper.class);
    private final CameraEndpointMapper endpoints = mock(CameraEndpointMapper.class);
    private final CameraCredentialMapper credentials = mock(CameraCredentialMapper.class);
    private final CameraSourceRules rules = mock(CameraSourceRules.class);
    private final StreamLocatorAdapter adapter = mock(StreamLocatorAdapter.class);
    private final CameraProfileEntity profile = new CameraProfileEntity();
    private final CameraSourceEntity source = new CameraSourceEntity();
    private final CameraEndpointEntity endpoint = new CameraEndpointEntity();
    private final CameraLocatorEntity locator = new CameraLocatorEntity();
    private final CameraCredentialEntity credential = new CameraCredentialEntity();
    private CameraCryptoService crypto;
    private PreviewLocatorService service;

    @BeforeEach
    void setup() {
        var properties = new CameraProperties();
        properties.setActiveKeyId("testkey");
        properties.setKeys(Map.of("testkey", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="));
        crypto = new CameraCryptoService(properties, new ObjectMapper().findAndRegisterModules());
        profile.setId(20L);
        profile.setSourceId(30L);
        profile.setChannelId(10L);
        profile.setVersion(0L);
        source.setId(30L);
        source.setEnabled(true);
        source.setVersion(0L);
        source.setNetworkPolicyKey("LAN");
        source.setRtspPort(554);
        source.setAdapterType("RTSP");
        endpoint.setId(31L);
        endpoint.setSourceId(30L);
        endpoint.setPurpose("RTSP");
        endpoint.setScheme("rtsp");
        // The reserved domain also makes unintended resolution an honest test failure.
        endpoint.setHost("camera.invalid");
        endpoint.setPort(554);
        endpoint.setBasePath("/");
        endpoint.setAuthMode("BASIC");
        endpoint.setCredentialId(40L);
        endpoint.setTlsPolicy("SYSTEM_CA");
        endpoint.setCreatedAt(LocalDateTime.of(2026, 10, 16, 0, 0));
        locator.setProfileId(20L);
        locator.setSourceId(30L);
        locator.setLocatorKind("RTSP");
        locator.setEndpointPurpose("RTSP");
        locator.setRtspHostMode("SOURCE");
        locator.setRtspTransport("TCP");
        locator.setIdentityDigest(new byte[32]);
        setLocatorSecret("/private-camera-path");
        credential.setId(40L);
        credential.setSourceId(30L);
        credential.setPurpose("RTSP");
        setCredentialSecret("private-password");
        when(locators.selectById(20L)).thenReturn(locator);
        when(sources.selectById(30L)).thenReturn(source);
        when(endpoints.selectOne(any())).thenReturn(endpoint);
        when(credentials.selectById(40L)).thenReturn(credential);
        when(adapter.type()).thenReturn("RTSP");
        service =
                new PreviewLocatorService(
                        locators, sources, endpoints, credentials, crypto, rules, List.of(adapter));
        // Adapter type registration is construction only, not connection-key work.
        clearInvocations(adapter);
    }

    @AfterEach
    void closeWithoutAnyAdapterOrDestinationRuleCalls() {
        service.close();
        verifyNoInteractions(adapter, rules);
    }

    @Test
    void identicalConfigurationReturnsAStableOpaqueKeyWithoutResolvingPlayback() {
        String first = service.connectionKey(profile);
        assertThat(service.connectionKey(profile)).isEqualTo(first);
        assertThat(first)
                .matches("testkey\\.[A-Za-z0-9_-]{43}")
                .doesNotContain(
                        "camera.invalid",
                        "private-camera-path",
                        "private-password",
                        "private-user");
        verify(locators, times(2)).selectById(20L);
        verify(sources, times(2)).selectById(30L);
        verify(endpoints, times(2)).selectOne(any());
        verify(credentials, times(2)).selectById(40L);
    }

    @Test
    void credentialCiphertextAndEncryptionMetadataInvalidateTheSharedConnectionKey() {
        assertChanges(() -> setCredentialSecret("replacement-password"));
        assertChanges(() -> credential.setSecretCiphertext(new byte[] {1, 2, 3}));
        assertChanges(() -> credential.setSecretNonce(new byte[12]));
        assertChanges(() -> credential.setSecretTag(new byte[16]));
        assertChanges(() -> credential.setEncryptionKeyId("retained-key"));
    }

    @Test
    void endpointAndTlsConfigurationChangesInvalidateTheKey() {
        assertChanges(() -> endpoint.setHost("replacement.invalid"));
        assertChanges(() -> endpoint.setPort(8554));
        assertChanges(() -> endpoint.setBasePath("/other"));
        assertChanges(() -> endpoint.setAuthMode("NONE"));
        assertChanges(() -> endpoint.setTlsPolicy("CUSTOM_CA"));
        assertChanges(() -> endpoint.setCaBundleKey("replacement-ca"));
        assertChanges(() -> endpoint.setCertSha256(new byte[32]));
    }

    @Test
    void locatorSecretsAndRoutingChangesInvalidateTheKey() {
        assertChanges(() -> setLocatorSecret("/replacement-path"));
        assertChanges(() -> locator.setRtspSecretCiphertext(new byte[] {4, 5, 6}));
        assertChanges(() -> locator.setRtspHostMode("EXPLICIT"));
        assertChanges(() -> locator.setRtspHost("different.invalid"));
        assertChanges(() -> locator.setRtspPort(9554));
        assertChanges(() -> locator.setRtspTransport("UDP"));
    }

    @Test
    void sourceAndProfileRevisionsAndNetworkConfigurationInvalidateTheKey() {
        assertChanges(() -> source.setVersion(1L));
        assertChanges(() -> profile.setVersion(1L));
        assertChanges(() -> source.setNetworkPolicyKey("OTHER_LAN"));
        assertChanges(() -> source.setRtspPort(8554));
    }

    @Test
    void disabledOrMissingSourceNeverGetsAReusableKey() {
        source.setEnabled(false);
        assertThatThrownBy(() -> service.connectionKey(profile))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("LOCATOR_NOT_CONFIGURED");
        when(sources.selectById(30L)).thenReturn(null);
        assertThatThrownBy(() -> service.connectionKey(profile))
                .hasMessage("LOCATOR_NOT_CONFIGURED");
        verifyNoInteractions(endpoints, credentials);
    }

    @Test
    void missingOrForeignCredentialsAreRejectedRatherThanSharingAnUnauthenticatedKey() {
        when(credentials.selectById(40L)).thenReturn(null);
        assertThatThrownBy(() -> service.connectionKey(profile))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("LOCATOR_INVALID");
        credential.setSourceId(99L);
        when(credentials.selectById(40L)).thenReturn(credential);
        assertThatThrownBy(() -> service.connectionKey(profile)).hasMessage("LOCATOR_INVALID");
    }

    @Test
    void missingLocatorEndpointAndForeignLocatorFailClosed() {
        when(locators.selectById(20L)).thenReturn(null);
        assertThatThrownBy(() -> service.connectionKey(profile))
                .hasMessage("LOCATOR_NOT_CONFIGURED");
        when(locators.selectById(20L)).thenReturn(locator);
        locator.setSourceId(99L);
        assertThatThrownBy(() -> service.connectionKey(profile)).hasMessage("LOCATOR_INVALID");
        locator.setSourceId(30L);
        when(endpoints.selectOne(any())).thenReturn(null);
        assertThatThrownBy(() -> service.connectionKey(profile))
                .hasMessage("LOCATOR_NOT_CONFIGURED");
    }

    private void assertChanges(Runnable mutation) {
        String before = service.connectionKey(profile);
        mutation.run();
        String after = service.connectionKey(profile);
        assertThat(after).isNotEqualTo(before);
        assertThat(service.connectionKey(profile)).isEqualTo(after);
    }

    private void setCredentialSecret(String password) {
        var secret =
                crypto.encrypt(
                        "camera_source_credential/30/40/RTSP",
                        Map.of("username", "private-user", "password", password),
                        16000);
        credential.setSecretCiphertext(secret.ciphertext());
        credential.setSecretNonce(secret.nonce());
        credential.setSecretTag(secret.tag());
        credential.setEncryptionKeyId(secret.keyId());
    }

    private void setLocatorSecret(String path) {
        var secret =
                crypto.encrypt(
                        "camera_profile_locator/30/20/RTSP",
                        Map.of("encodedPath", path, "rawQuery", ""),
                        16000);
        locator.setRtspSecretCiphertext(secret.ciphertext());
        locator.setRtspSecretNonce(secret.nonce());
        locator.setRtspSecretTag(secret.tag());
        locator.setRtspEncryptionKeyId(secret.keyId());
    }
}
