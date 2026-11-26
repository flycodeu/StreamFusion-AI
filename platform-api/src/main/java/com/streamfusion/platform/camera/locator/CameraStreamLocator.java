package com.streamfusion.platform.camera.locator;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
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
import java.net.URI;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.function.LongSupplier;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** Owns profile connection identity, stored credentials and on-demand protocol resolution. */
@Service
@RequiredArgsConstructor
public class CameraStreamLocator {
    private final CameraLocatorMapper locators;
    private final CameraSourceMapper sources;
    private final CameraEndpointMapper endpoints;
    private final CameraCredentialMapper credentials;
    private final CameraCryptoService crypto;
    private final StreamLocatorAdapterRegistry adapters;

    /** Stable local configuration identity, independent of expiring vendor URLs. */
    public String connectionKey(CameraProfileEntity profile) {
        return connectionKey(profile, readConfiguration(profile));
    }

    private String connectionKey(
            CameraProfileEntity profile, ConnectionConfiguration configuration) {
        var source = configuration.source();
        var snapshot = new LinkedHashMap<String, Object>();
        snapshot.put("profileId", profile.getId());
        snapshot.put("sourceId", profile.getSourceId());
        snapshot.put("channelId", profile.getChannelId());
        snapshot.put("profileVersion", profile.getVersion());
        snapshot.put("sourceVersion", source.getVersion());
        snapshot.put("networkPolicy", source.getNetworkPolicyKey());
        snapshot.put("rtspPort", source.getRtspPort());
        snapshot.put("locator", configuration.locator());
        snapshot.put("endpoint", configuration.endpoint());
        if (configuration.credential() != null)
            snapshot.put("credential", configuration.credential());
        // Only the HMAC is retained. Neither plaintext credentials nor vendor URLs are cached.
        return crypto.sign("preview-connection", snapshot);
    }

    /** Resolve stored configuration, checking its identity when a connection was reserved. */
    public Target resolve(
            CameraProfileEntity profile,
            String expectedConnectionKey,
            Runnable authorization,
            LongSupplier deadlineNanos) {
        deadlineNanos.getAsLong();
        authorization.run();
        var configuration = readConfiguration(profile);
        // Validate the exact rows that will be used, before decrypting or contacting the source.
        if (expectedConnectionKey != null
                && !expectedConnectionKey.equals(connectionKey(profile, configuration)))
            throw new CameraAdapterException("CONFIGURATION_CHANGED");
        deadlineNanos.getAsLong();
        var locator = configuration.locator();
        var source = configuration.source();
        String kind = locator.getLocatorKind();
        var adapter = adapters.required(kind);
        var endpoint = configuration.endpoint();
        String username = null, password = null;
        var credential = configuration.credential();
        if (credential != null) {
            JsonNode secret =
                    crypto.decrypt(
                            "camera_source_credential/"
                                    + credential.getSourceId()
                                    + "/"
                                    + credential.getId()
                                    + "/"
                                    + credential.getPurpose(),
                            new CameraCryptoService.Envelope(
                                    credential.getEncryptionKeyId(),
                                    credential.getSecretNonce(),
                                    credential.getSecretCiphertext(),
                                    credential.getSecretTag()));
            username = secret.path("username").asText();
            password = secret.path("password").asText();
        }
        try {
            URI control =
                    new URI(
                            endpoint.getScheme(),
                            null,
                            endpoint.getHost(),
                            endpoint.getPort(),
                            endpoint.getBasePath() == null ? "/" : endpoint.getBasePath(),
                            null,
                            null);
            var context =
                    new CameraAccessContext(
                            control,
                            source.getNetworkPolicyKey(),
                            username,
                            password,
                            source.getRtspPort() == null ? 554 : source.getRtspPort(),
                            () -> {
                                deadlineNanos.getAsLong();
                                authorization.run();
                            },
                            deadlineNanos.getAsLong());
            boolean rtsp = "RTSP".equals(kind);
            JsonNode config =
                    crypto.decrypt(
                            "camera_profile_locator/"
                                    + locator.getSourceId()
                                    + "/"
                                    + locator.getProfileId()
                                    + "/"
                                    + kind,
                            rtsp
                                    ? new CameraCryptoService.Envelope(
                                            locator.getRtspEncryptionKeyId(),
                                            locator.getRtspSecretNonce(),
                                            locator.getRtspSecretCiphertext(),
                                            locator.getRtspSecretTag())
                                    : new CameraCryptoService.Envelope(
                                            locator.getProtocolEncryptionKeyId(),
                                            locator.getProtocolSecretNonce(),
                                            locator.getProtocolSecretCiphertext(),
                                            locator.getProtocolSecretTag()));
            URI uri = adapter.resolve(locator, config, context);
            deadlineNanos.getAsLong();
            if (uri == null
                    || !(CameraStreamProtocol.supports(kind)
                            ? CameraStreamProtocol.valueOf(kind).accepts(uri.getScheme())
                            : "rtsp".equalsIgnoreCase(uri.getScheme()))
                    || uri.getRawUserInfo() != null
                    || uri.getFragment() != null
                    || uri.getHost() == null
                    || uri.toString().length() > 8192)
                throw new CameraAdapterException("LOCATOR_INVALID");
            String userInfo = "";
            if (!"HIK_PLATFORM".equals(kind)
                    && !CameraStreamProtocol.ingress(kind)
                    && username != null
                    && !username.isEmpty()) {
                userInfo = encode(username) + ":" + encode(password == null ? "" : password) + "@";
            }
            return new Target(kind, uri, source.getNetworkPolicyKey(), userInfo);
        } catch (CameraAdapterException ex) {
            deadlineNanos.getAsLong();
            throw ex;
        } catch (java.net.URISyntaxException | IllegalArgumentException ex) {
            throw new CameraAdapterException("LOCATOR_INVALID");
        }
    }

    private ConnectionConfiguration readConfiguration(CameraProfileEntity profile) {
        var locator = locators.selectById(profile.getId());
        var source = sources.selectById(profile.getSourceId());
        if (locator == null || source == null || !Boolean.TRUE.equals(source.getEnabled()))
            throw new CameraAdapterException("LOCATOR_NOT_CONFIGURED");
        if (!profile.getSourceId().equals(locator.getSourceId()))
            throw new CameraAdapterException("LOCATOR_INVALID");
        var endpoint = endpoint(profile.getSourceId(), locator);
        return new ConnectionConfiguration(locator, source, endpoint, credential(source, endpoint));
    }

    private record ConnectionConfiguration(
            CameraLocatorEntity locator,
            CameraSourceEntity source,
            CameraEndpointEntity endpoint,
            CameraCredentialEntity credential) {
        @Override
        public String toString() {
            return "ConnectionConfiguration[redacted]";
        }
    }

    private CameraEndpointEntity endpoint(long sourceId, CameraLocatorEntity locator) {
        var endpoint =
                endpoints.selectOne(
                        new LambdaQueryWrapper<CameraEndpointEntity>()
                                .eq(CameraEndpointEntity::getSourceId, sourceId)
                                .eq(
                                        CameraEndpointEntity::getPurpose,
                                        locator.getEndpointPurpose()));
        if (endpoint == null) throw new CameraAdapterException("LOCATOR_NOT_CONFIGURED");
        return endpoint;
    }

    private CameraCredentialEntity credential(
            CameraSourceEntity source, CameraEndpointEntity endpoint) {
        if (endpoint.getCredentialId() == null) return null;
        var credential = credentials.selectById(endpoint.getCredentialId());
        if (credential == null || !source.getId().equals(credential.getSourceId()))
            throw new CameraAdapterException("LOCATOR_INVALID");
        return credential;
    }

    private static String encode(String value) {
        return URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20");
    }

    /** In-memory only; raw vendor control credentials stay inside the locator. */
    @RequiredArgsConstructor(access = lombok.AccessLevel.PRIVATE)
    public static final class Target {
        private final String kind;
        private final URI uri;
        private final String networkPolicyKey;
        private final String userInfo;

        public String kind() {
            return kind;
        }

        public URI uri() {
            return uri;
        }

        public String networkPolicyKey() {
            return networkPolicyKey;
        }

        public String authorityAt(String pinnedAddress, int port) {
            return userInfo
                    + (pinnedAddress.contains(":") ? "[" + pinnedAddress + "]" : pinnedAddress)
                    + ":"
                    + port;
        }

        @Override
        public String toString() {
            return "Target[redacted]";
        }
    }
}
