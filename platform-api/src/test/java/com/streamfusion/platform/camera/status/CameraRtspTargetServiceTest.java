package com.streamfusion.platform.camera.status;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.mapper.CameraCredentialMapper;
import com.streamfusion.platform.camera.mapper.CameraEndpointMapper;
import com.streamfusion.platform.camera.mapper.CameraLocatorMapper;
import com.streamfusion.platform.camera.mapper.CameraSourceMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraCredentialEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraEndpointEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraSourceEntity;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.media.preview.source.DahuaStreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.HikvisionStreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.OnvifStreamLocatorAdapter;
import com.streamfusion.platform.media.preview.source.StreamLocatorAdapterRegistry;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/** Real AES-GCM/HMAC semantics against local records only; inspect never contacts a network. */
class CameraRtspTargetServiceTest {
    private CameraRtspTargetService service;
    private CameraCryptoService crypto;
    private CameraProperties properties;
    private CameraCredentialMapper credentials;
    private CameraLocatorMapper locators;
    private CameraSourceMapper sources;
    private CameraEndpointMapper endpoints;
    private CameraChannelEntity camera;
    private CameraProfileEntity profile;
    private CameraSourceEntity source;
    private CameraEndpointEntity endpoint;
    private CameraLocatorEntity locator;
    private OnvifStreamLocatorAdapter onvif;

    @BeforeEach
    void setup() {
        properties = new CameraProperties();
        properties.setActiveKeyId("test");
        properties.setKeys(Map.of("test", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="));
        var policy = new CameraProperties.NetworkPolicy();
        policy.setCidrs(List.of("10.0.0.0/8"));
        policy.setName("Fixture policy");
        properties.setNetworkPolicies(Map.of("fixture", policy));
        crypto = new CameraCryptoService(properties, new ObjectMapper());
        locators = mock(CameraLocatorMapper.class);
        sources = mock(CameraSourceMapper.class);
        endpoints = mock(CameraEndpointMapper.class);
        credentials = mock(CameraCredentialMapper.class);
        onvif = mock(OnvifStreamLocatorAdapter.class);
        when(onvif.type()).thenReturn("ONVIF");
        service =
                new CameraRtspTargetService(
                        locators,
                        sources,
                        endpoints,
                        credentials,
                        crypto,
                        new CameraSourceRules(properties),
                        properties,
                        new CameraStatusStreamResolver(
                                new StreamLocatorAdapterRegistry(
                                        List.of(
                                                onvif,
                                                new HikvisionStreamLocatorAdapter(),
                                                new DahuaStreamLocatorAdapter()))));
        clearInvocations(onvif);
        camera = new CameraChannelEntity();
        camera.setId(2L);
        camera.setSourceId(1L);
        camera.setLifecycle("ENABLED");
        profile = new CameraProfileEntity();
        profile.setId(3L);
        profile.setChannelId(2L);
        profile.setSourceId(1L);
        profile.setEnabled(true);
        source = new CameraSourceEntity();
        source.setId(1L);
        source.setName("Fixture source");
        source.setAdapterType("RTSP");
        source.setEnabled(true);
        source.setNetworkPolicyKey("fixture");
        endpoint = new CameraEndpointEntity();
        endpoint.setSourceId(1L);
        endpoint.setPurpose("RTSP");
        endpoint.setScheme("rtsp");
        endpoint.setHost("10.0.1.5");
        endpoint.setPort(554);
        endpoint.setAuthMode("NONE");
        locator = new CameraLocatorEntity();
        locator.setProfileId(3L);
        locator.setSourceId(1L);
        locator.setLocatorKind("RTSP");
        locator.setEndpointPurpose("RTSP");
        locator.setRtspHostMode("SOURCE");
        locator.setRtspTransport("TCP");
        encryptLocator("/Streaming/%63hannel/1", "token=a%2Fb&n=1");
        when(locators.selectById(3L)).thenReturn(locator);
        when(sources.selectById(1L)).thenReturn(source);
        when(endpoints.selectOne(any())).thenReturn(endpoint);
    }

    @Test
    void inspectionPreservesRawPathAndQueryWithoutUserinfoOrCallingAuthorization() {
        Runnable auth = mock(Runnable.class);
        var result = service.inspect(camera, profile, "test", auth);
        assertThat(result.blockingReasonCode()).isNull();
        assertThat(result.target().uri().toASCIIString())
                .isEqualTo("rtsp://10.0.1.5:554/Streaming/%63hannel/1?token=a%2Fb&n=1");
        assertThat(result.target().uri().getRawUserInfo()).isNull();
        assertThat(result.toString()).isEqualTo("Inspection[redacted]");
        assertThat(result.target().toString()).isEqualTo("Target[redacted]");
        verifyNoInteractions(auth);
    }

    @Test
    void cosmeticEditsAndSamePlaintextReEncryptionKeepFingerprint() {
        var before = inspect().connectionDigest();
        byte[] oldNonce = locator.getRtspSecretNonce().clone();
        camera.setName("Renamed camera");
        camera.setVersion(11L);
        profile.setLabel("Renamed profile");
        profile.setVersion(12L);
        source.setName("Renamed source");
        source.setVersion(13L);
        encryptLocator("/Streaming/%63hannel/1", "token=a%2Fb&n=1");
        assertThat(locator.getRtspSecretNonce()).isNotEqualTo(oldNonce);
        assertThat(inspect().connectionDigest()).isEqualTo(before);
    }

    @Test
    void actualPathPasswordEndpointPolicyAndEnabledChangesInvalidateFingerprint() {
        var before = inspect().connectionDigest();
        encryptLocator("/Streaming/changed", "token=a%2Fb&n=1");
        assertThat(inspect().connectionDigest()).isNotEqualTo(before);
        encryptLocator("/Streaming/%63hannel/1", "token=a%2Fb&n=1");
        endpoint.setPort(8554);
        assertThat(inspect().connectionDigest()).isNotEqualTo(before);
        endpoint.setPort(554);
        properties.getNetworkPolicies().get("fixture").setCidrs(List.of("10.0.0.0/16"));
        assertThat(inspect().connectionDigest()).isNotEqualTo(before);
        properties.getNetworkPolicies().get("fixture").setCidrs(List.of("10.0.0.0/8"));
        source.setEnabled(false);
        assertThat(inspect().blockingReasonCode()).isEqualTo("SOURCE_DISABLED");
        assertThat(inspect().connectionDigest()).isNotEqualTo(before);
        source.setEnabled(true);
        endpoint.setAuthMode("DRIVER_NEGOTIATED");
        endpoint.setCredentialId(4L);
        var credential = credential("operator", "first secret");
        when(credentials.selectById(4L)).thenReturn(credential);
        var authBefore = inspect().connectionDigest();
        credential = credential("operator", "second secret");
        when(credentials.selectById(4L)).thenReturn(credential);
        assertThat(inspect().connectionDigest()).isNotEqualTo(authBefore);
    }

    @Test
    void nullCredentialOrWrongPurposeIsBlockedBeforeDecryption() {
        endpoint.setAuthMode("DRIVER_NEGOTIATED");
        assertThat(inspect().blockingReasonCode()).isEqualTo("LOCATOR_INVALID");
        verifyNoInteractions(credentials);
        endpoint.setCredentialId(4L);
        var credential = credential("operator", "sensitive");
        credential.setPurpose("DEVICE_HTTP");
        when(credentials.selectById(4L)).thenReturn(credential);
        assertThat(inspect().blockingReasonCode()).isEqualTo("LOCATOR_INVALID");
    }

    @Test
    void mismatchedSourcePlatformAndNonTcpLocatorsNeverProduceRtspTargets() {
        locator.setLocatorKind("ONVIF");
        assertThat(inspect().blockingReasonCode()).isEqualTo("SOURCE_PROTOCOL_NOT_SUPPORTED");
        assertThat(inspect().target()).isNull();
        locator.setLocatorKind("PLATFORM");
        assertThat(inspect().target()).isNull();
        locator.setLocatorKind("RTSP");
        locator.setRtspTransport("UDP");
        assertThat(inspect().target()).isNull();
        verifyNoInteractions(credentials);
    }

    @Test
    void disabledProfileAndChannelHaveDistinctBlockingReasons() {
        profile.setEnabled(false);
        assertThat(inspect().blockingReasonCode()).isEqualTo("PROFILE_DISABLED");
        profile.setEnabled(true);
        camera.setLifecycle("DISABLED");
        assertThat(inspect().blockingReasonCode()).isEqualTo("CHANNEL_DISABLED");
    }

    @Test
    void badCiphertextRemovedKeyAndWrongProfileAreControlledFailures() {
        assertThat(service.inspect(camera, profile, "removed-key", () -> {}).blockingReasonCode())
                .isEqualTo("CONFIGURATION_UNAVAILABLE");
        locator.setRtspSecretTag(new byte[16]);
        assertThat(inspect().blockingReasonCode()).isEqualTo("CONFIGURATION_UNAVAILABLE");
        profile.setChannelId(99L);
        assertThat(inspect().blockingReasonCode()).isEqualTo("TARGET_DELETED");
        assertThat(service.inspect(camera, null, "test", () -> {}).blockingReasonCode())
                .isEqualTo("TARGET_DELETED");
    }

    @Test
    void batchLoadsConnectionRowsOnceForBothCurrentAndObservationKeys() {
        properties.setKeys(
                Map.of(
                        "test",
                        "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
                        "old",
                        "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="));
        endpoint.setAuthMode("DRIVER_NEGOTIATED");
        endpoint.setCredentialId(4L);
        var credential = credential("operator", "batch-secret");
        var extra = new CameraProfileEntity();
        extra.setId(5L);
        extra.setChannelId(2L);
        extra.setSourceId(1L);
        extra.setEnabled(true);
        var extraLocator = new CameraLocatorEntity();
        extraLocator.setProfileId(5L);
        extraLocator.setSourceId(1L);
        extraLocator.setLocatorKind("RTSP");
        extraLocator.setEndpointPurpose("RTSP");
        extraLocator.setRtspHostMode("SOURCE");
        extraLocator.setRtspTransport("TCP");
        var envelope =
                crypto.encrypt(
                        "camera_profile_locator/1/5/RTSP",
                        Map.of("encodedPath", "/substream", "rawQuery", ""),
                        8192);
        extraLocator.setRtspEncryptionKeyId(envelope.keyId());
        extraLocator.setRtspSecretCiphertext(envelope.ciphertext());
        extraLocator.setRtspSecretNonce(envelope.nonce());
        extraLocator.setRtspSecretTag(envelope.tag());
        when(sources.selectBatchIds(anyCollection())).thenReturn(List.of(source));
        when(locators.selectBatchIds(anyCollection())).thenReturn(List.of(locator, extraLocator));
        when(endpoints.selectBySources(any())).thenReturn(List.of(endpoint));
        when(credentials.selectBatchIds(anyCollection())).thenReturn(List.of(credential));
        clearInvocations(sources, locators, endpoints, credentials);
        var result =
                service.inspectBatch(
                        List.of(camera),
                        List.of(profile, extra),
                        "test",
                        Map.of(3L, "old", 5L, "test"));
        assertThat(result.current()).hasSize(2);
        assertThat(result.observed()).hasSize(2);
        assertThat(result.current().get(3L).blockingReasonCode()).isNull();
        assertThat(result.observed().get(3L).connectionDigest())
                .isNotEqualTo(result.current().get(3L).connectionDigest());
        assertThat(result.observed().get(5L)).isSameAs(result.current().get(5L));
        verify(sources, times(1)).selectBatchIds(anyCollection());
        verify(locators, times(1)).selectBatchIds(anyCollection());
        verify(endpoints, times(1)).selectBySources(any());
        verify(credentials, times(1)).selectBatchIds(anyCollection());
        verify(sources, never()).selectById(any(java.io.Serializable.class));
        verify(locators, never()).selectById(any(java.io.Serializable.class));
        verify(endpoints, never()).selectOne(any());
        verify(credentials, never()).selectById(any(java.io.Serializable.class));
    }

    @Test
    void absentBatchCredentialDoesNotFallbackToPerProfileDatabaseCalls() {
        endpoint.setAuthMode("DRIVER_NEGOTIATED");
        endpoint.setCredentialId(4L);
        when(sources.selectBatchIds(anyCollection())).thenReturn(List.of(source));
        when(locators.selectBatchIds(anyCollection())).thenReturn(List.of(locator));
        when(endpoints.selectBySources(any())).thenReturn(List.of(endpoint));
        when(credentials.selectBatchIds(anyCollection())).thenReturn(List.of());
        clearInvocations(sources, locators, endpoints, credentials);
        var result =
                service.inspectBatch(List.of(camera), List.of(profile), "test", Map.of(3L, "test"));
        assertThat(result.current().get(3L).blockingReasonCode()).isEqualTo("LOCATOR_INVALID");
        assertThat(result.observed().get(3L).target()).isNull();
        verify(credentials, times(1)).selectBatchIds(anyCollection());
        verify(credentials, never()).selectById(any(java.io.Serializable.class));
    }

    private CameraRtspTargetService.Inspection inspect() {
        return service.inspect(camera, profile, "test", () -> {});
    }

    @org.junit.jupiter.params.ParameterizedTest
    @org.junit.jupiter.params.provider.CsvSource({
        "HIKVISION,/Streaming/Channels/101",
        "DAHUA,/cam/realmonitor?channel=3&subtype=1"
    })
    void nativeLocatorsReuseExactImportedIdentityAndConfiguredRtspPort(String kind, String path) {
        protocol(kind, Map.of("profileToken", "101", "channelKey", "Encode[2]", "streamType", 1));
        var result = inspect();
        assertThat(result.blockingReasonCode()).isNull();
        assertThat(result.targetResolver()).isNull();
        assertThat(result.target().uri().toString()).isEqualTo("rtsp://10.0.1.5:8554" + path);
        verifyNoInteractions(onvif);
    }

    @Test
    void onvifInspectionAndBatchAreLocalAndFingerprintUsesStableProtocolConfiguration() {
        var config =
                Map.of(
                        "profileToken",
                        "opaque-main",
                        "serviceNamespace",
                        "http://www.onvif.org/ver20/media/wsdl",
                        "serviceEndpoint",
                        "http://10.0.1.5/onvif/media");
        protocol("ONVIF", config);
        var first = inspect();
        assertThat(first.blockingReasonCode()).isNull();
        assertThat(first.targetResolver()).isNotNull();
        protocol("ONVIF", config);
        assertThat(inspect().connectionDigest()).isEqualTo(first.connectionDigest());
        source.setRtspPort(554);
        assertThat(inspect().connectionDigest()).isNotEqualTo(first.connectionDigest());
        source.setRtspPort(8554);
        when(sources.selectBatchIds(anyCollection())).thenReturn(List.of(source));
        when(locators.selectBatchIds(anyCollection())).thenReturn(List.of(locator));
        when(endpoints.selectBySources(any())).thenReturn(List.of(endpoint));
        var batch = service.inspectBatch(List.of(camera), List.of(profile), "test", Map.of());
        assertThat(batch.current().get(3L).blockingReasonCode()).isNull();
        assertThat(batch.current().get(3L).connectionDigest()).isEqualTo(first.connectionDigest());
        verifyNoInteractions(onvif);
        protocol(
                "ONVIF",
                Map.of(
                        "profileToken",
                        "changed",
                        "serviceNamespace",
                        "http://www.onvif.org/ver20/media/wsdl",
                        "serviceEndpoint",
                        "http://10.0.1.5/onvif/media"));
        assertThat(inspect().connectionDigest()).isNotEqualTo(first.connectionDigest());
    }

    @Test
    void incompleteOnvifAndCrossHostMediaServiceAreNotRunnable() {
        protocol("ONVIF", Map.of("profileToken", "opaque-main"));
        assertThat(inspect().blockingReasonCode()).isEqualTo("ONVIF_MEDIA_NOT_CONFIGURED");
        protocol(
                "ONVIF",
                Map.of(
                        "profileToken",
                        "opaque-main",
                        "serviceNamespace",
                        "http://www.onvif.org/ver10/media/wsdl",
                        "serviceEndpoint",
                        "http://10.0.1.6/onvif/media"));
        assertThat(inspect().blockingReasonCode()).isEqualTo("LOCATOR_INVALID");
        verifyNoInteractions(onvif);
    }

    @Test
    void deferredOnvifUsesWorkerDeadlineAndDoesNotPersistOrFingerprintReturnedUri() {
        protocol(
                "ONVIF",
                Map.of(
                        "profileToken",
                        "opaque-main",
                        "serviceNamespace",
                        "http://www.onvif.org/ver10/media/wsdl",
                        "serviceEndpoint",
                        "http://10.0.1.5/onvif/media"));
        var first = inspect();
        var deadline = System.nanoTime() + java.util.concurrent.TimeUnit.SECONDS.toNanos(1);
        Runnable active = mock(Runnable.class);
        when(onvif.resolve(any(), any(), any()))
                .thenAnswer(
                        call -> {
                            com.streamfusion.platform.camera.access.adapter.CameraAccessContext
                                    context = call.getArgument(2);
                            assertThat(context.deadlineNanos()).isEqualTo(deadline);
                            assertThat(context.checkAuthorization()).isSameAs(active);
                            return java.net.URI.create(
                                    "rtsp://10.0.1.5:9554/temporary?token=secret");
                        });
        var resolved = first.targetResolver().resolve(deadline, active);
        assertThat(resolved.uri().getPort()).isEqualTo(9554);
        assertThat(inspect().connectionDigest()).isEqualTo(first.connectionDigest());
        verify(locators, never()).updateById(any(CameraLocatorEntity.class));
        verify(active, times(2)).run();
    }

    private void protocol(String kind, Map<String, ?> config) {
        source.setAdapterType(kind);
        source.setRtspPort(8554);
        endpoint.setPurpose("ONVIF".equals(kind) ? "ONVIF" : "VENDOR_HTTP");
        endpoint.setScheme("http");
        endpoint.setPort(80);
        locator.setLocatorKind(kind);
        locator.setEndpointPurpose(endpoint.getPurpose());
        var envelope = crypto.encrypt("camera_profile_locator/1/3/" + kind, config, 16000);
        locator.setProtocolEncryptionKeyId(envelope.keyId());
        locator.setProtocolSecretCiphertext(envelope.ciphertext());
        locator.setProtocolSecretNonce(envelope.nonce());
        locator.setProtocolSecretTag(envelope.tag());
    }

    private void encryptLocator(String path, String query) {
        var envelope =
                crypto.encrypt(
                        "camera_profile_locator/1/3/RTSP",
                        Map.of("encodedPath", path, "rawQuery", query),
                        8192);
        locator.setRtspEncryptionKeyId(envelope.keyId());
        locator.setRtspSecretCiphertext(envelope.ciphertext());
        locator.setRtspSecretNonce(envelope.nonce());
        locator.setRtspSecretTag(envelope.tag());
    }

    private CameraCredentialEntity credential(String username, String password) {
        var credential = new CameraCredentialEntity();
        credential.setId(4L);
        credential.setSourceId(1L);
        credential.setPurpose("RTSP");
        var envelope =
                crypto.encrypt(
                        "camera_source_credential/1/4/RTSP",
                        Map.of("username", username, "password", password),
                        8192);
        credential.setEncryptionKeyId(envelope.keyId());
        credential.setSecretCiphertext(envelope.ciphertext());
        credential.setSecretNonce(envelope.nonce());
        credential.setSecretTag(envelope.tag());
        return credential;
    }
}
