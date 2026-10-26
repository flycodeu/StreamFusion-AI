package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
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
import com.streamfusion.platform.camera.status.CameraStatusStreamResolver;
import com.streamfusion.platform.camera.status.CameraStatusWorker;
import com.streamfusion.platform.camera.status.RtspDescribeProbe;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.media.preview.PreviewFailure;
import java.net.URI;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** Read-only configuration inspection. DNS and ONVIF resolution run only in the status worker. */
@Service
@RequiredArgsConstructor
public class CameraRtspTargetService {
    private final CameraLocatorMapper locators;
    private final CameraSourceMapper sources;
    private final CameraEndpointMapper endpoints;
    private final CameraCredentialMapper credentials;
    private final CameraCryptoService crypto;
    private final CameraSourceRules rules;
    private final CameraProperties properties;
    private final CameraStatusStreamResolver streams;

    public com.streamfusion.platform.camera.status.CameraStatusLayer effectiveLayer(
            long profileId, com.streamfusion.platform.camera.status.CameraStatusLayer requested) {
        var locator = locators.selectById(profileId);
        if (locator != null && CameraStreamProtocol.stream(locator.getLocatorKind()))
            return com.streamfusion.platform.camera.status.CameraStatusLayer.MEDIA;
        return locator != null && "HIK_PLATFORM".equals(locator.getLocatorKind())
                ? com.streamfusion.platform.camera.status.CameraStatusLayer.UPSTREAM
                : requested == com.streamfusion.platform.camera.status.CameraStatusLayer.UPSTREAM
                        ? com.streamfusion.platform.camera.status.CameraStatusLayer.CONTROL
                        : requested;
    }

    public java.util.Set<Long> platformProfiles(List<Long> profileIds) {
        if (profileIds.isEmpty()) return java.util.Set.of();
        return locators.selectByIds(profileIds).stream()
                .filter(l -> "HIK_PLATFORM".equals(l.getLocatorKind()))
                .map(com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity::getProfileId)
                .collect(java.util.stream.Collectors.toSet());
    }

    public java.util.Set<Long> streamProfiles(List<Long> profileIds) {
        if (profileIds.isEmpty()) return java.util.Set.of();
        return locators.selectByIds(profileIds).stream()
                .filter(l -> CameraStreamProtocol.stream(l.getLocatorKind()))
                .map(CameraLocatorEntity::getProfileId)
                .collect(java.util.stream.Collectors.toSet());
    }

    private CameraEndpointEntity endpoint(long sourceId, String purpose) {
        return endpoints.selectOne(
                new LambdaQueryWrapper<CameraEndpointEntity>()
                        .eq(CameraEndpointEntity::getSourceId, sourceId)
                        .eq(CameraEndpointEntity::getPurpose, purpose));
    }

    public record Inspection(
            RtspDescribeProbe.Target target,
            byte[] connectionDigest,
            String blockingReasonCode,
            CameraStatusWorker.TargetResolver targetResolver) {
        public Inspection(
                RtspDescribeProbe.Target target,
                byte[] connectionDigest,
                String blockingReasonCode) {
            this(target, connectionDigest, blockingReasonCode, null);
        }

        @Override
        public String toString() {
            return "Inspection[redacted]";
        }
    }

    public Inspection inspect(
            CameraChannelEntity camera,
            CameraProfileEntity profile,
            String keyId,
            Runnable authorizationCheck) {
        if (camera == null
                || profile == null
                || !camera.getId().equals(profile.getChannelId())
                || !camera.getSourceId().equals(profile.getSourceId()))
            return blocked("TARGET_DELETED");
        var source = sources.selectById(profile.getSourceId());
        var locator = locators.selectById(profile.getId());
        var endpoint =
                locator == null
                        ? null
                        : endpoint(profile.getSourceId(), locator.getEndpointPurpose());
        return inspectLoaded(
                camera, profile, keyId, authorizationCheck, source, locator, endpoint, null, false);
    }

    public record PageInspections(Map<Long, Inspection> current, Map<Long, Inspection> observed) {}

    /** A request-local read: four SQL batches, shared by current-key and observation-key checks. */
    public PageInspections inspectBatch(
            List<? extends CameraChannelEntity> cameras,
            List<CameraProfileEntity> profiles,
            String activeKeyId,
            Map<Long, String> observationKeys) {
        if (profiles == null || profiles.isEmpty()) return new PageInspections(Map.of(), Map.of());
        var cameraById = new HashMap<Long, CameraChannelEntity>();
        for (var camera : cameras) if (camera != null) cameraById.put(camera.getId(), camera);
        var profileIds = profiles.stream().map(CameraProfileEntity::getId).distinct().toList();
        var sourceIds = profiles.stream().map(CameraProfileEntity::getSourceId).distinct().toList();
        var sourceById = new HashMap<Long, CameraSourceEntity>();
        for (var source : sources.selectBatchIds(sourceIds)) sourceById.put(source.getId(), source);
        var locatorByProfile = new HashMap<Long, CameraLocatorEntity>();
        for (var locator : locators.selectBatchIds(profileIds))
            locatorByProfile.put(locator.getProfileId(), locator);
        var endpointBySource = new HashMap<String, CameraEndpointEntity>();
        for (var endpoint : endpoints.selectBySources(sourceIds))
            endpointBySource.put(endpoint.getSourceId() + "/" + endpoint.getPurpose(), endpoint);
        var credentialIds =
                endpointBySource.values().stream()
                        .map(CameraEndpointEntity::getCredentialId)
                        .filter(Objects::nonNull)
                        .distinct()
                        .toList();
        var credentialById = new HashMap<Long, CameraCredentialEntity>();
        if (!credentialIds.isEmpty())
            for (var credential : credentials.selectBatchIds(credentialIds))
                credentialById.put(credential.getId(), credential);
        var current = new HashMap<Long, Inspection>();
        var observed = new HashMap<Long, Inspection>();
        for (var profile : profiles) {
            var camera = cameraById.get(profile.getChannelId());
            var locator = locatorByProfile.get(profile.getId());
            var endpoint =
                    locator == null
                            ? null
                            : endpointBySource.get(
                                    profile.getSourceId() + "/" + locator.getEndpointPurpose());
            var credential =
                    endpoint == null || endpoint.getCredentialId() == null
                            ? null
                            : credentialById.get(endpoint.getCredentialId());
            current.put(
                    profile.getId(),
                    activeKeyId == null
                            ? blocked("CONFIGURATION_UNAVAILABLE")
                            : inspectLoaded(
                                    camera,
                                    profile,
                                    activeKeyId,
                                    () -> {},
                                    sourceById.get(profile.getSourceId()),
                                    locatorByProfile.get(profile.getId()),
                                    endpoint,
                                    credential,
                                    true));
            String observedKey = observationKeys.get(profile.getId());
            if (observedKey != null)
                observed.put(
                        profile.getId(),
                        Objects.equals(activeKeyId, observedKey)
                                ? current.get(profile.getId())
                                : inspectLoaded(
                                        camera,
                                        profile,
                                        observedKey,
                                        () -> {},
                                        sourceById.get(profile.getSourceId()),
                                        locatorByProfile.get(profile.getId()),
                                        endpoint,
                                        credential,
                                        true));
        }
        return new PageInspections(current, observed);
    }

    private Inspection inspectLoaded(
            CameraChannelEntity camera,
            CameraProfileEntity profile,
            String keyId,
            Runnable authorizationCheck,
            CameraSourceEntity source,
            CameraLocatorEntity locator,
            CameraEndpointEntity endpoint,
            CameraCredentialEntity preloadedCredential,
            boolean batchLoaded) {
        if (camera == null
                || profile == null
                || !camera.getId().equals(profile.getChannelId())
                || !camera.getSourceId().equals(profile.getSourceId()))
            return blocked("TARGET_DELETED");
        if (source == null) return blocked("CONFIGURATION_UNAVAILABLE");
        if (locator == null)
            return blocked(
                    ("RTSP".equals(source.getAdapterType())
                                    || streams.supports(source.getAdapterType()))
                            ? "LOCATOR_NOT_CONFIGURED"
                            : "SOURCE_PROTOCOL_NOT_SUPPORTED");
        if (!profile.getSourceId().equals(locator.getSourceId())) return blocked("LOCATOR_INVALID");
        boolean direct = "RTSP".equals(locator.getLocatorKind());
        boolean platform = "HIK_PLATFORM".equals(locator.getLocatorKind());
        if (!direct
                && (!streams.supports(locator.getLocatorKind())
                        || !locator.getLocatorKind().equals(source.getAdapterType())))
            return blocked("SOURCE_PROTOCOL_NOT_SUPPORTED");
        if (direct
                && (!"RTSP".equals(locator.getEndpointPurpose())
                        || !"TCP".equals(locator.getRtspTransport())))
            return blocked("SOURCE_PROTOCOL_NOT_SUPPORTED");
        if (endpoint == null)
            return blocked(direct ? "RTSP_ENDPOINT_NOT_CONFIGURED" : "LOCATOR_NOT_CONFIGURED");
        if (!source.getId().equals(endpoint.getSourceId())
                || !Objects.equals(locator.getEndpointPurpose(), endpoint.getPurpose())
                || !(CameraStreamProtocol.stream(locator.getLocatorKind())
                        ? CameraStreamProtocol.valueOf(locator.getLocatorKind())
                                .accepts(endpoint.getScheme())
                        : direct
                                ? "rtsp".equals(endpoint.getScheme())
                                : "http".equals(endpoint.getScheme())
                                        || "https".equals(endpoint.getScheme())))
            return blocked("LOCATOR_INVALID");
        try {
            var config =
                    crypto.decrypt(
                            "camera_profile_locator/"
                                    + source.getId()
                                    + "/"
                                    + profile.getId()
                                    + "/"
                                    + locator.getLocatorKind(),
                            direct
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
            String username = null, password = null, credentialPurpose = null;
            if ("NONE".equals(endpoint.getAuthMode())) {
                if (endpoint.getCredentialId() != null) return blocked("LOCATOR_INVALID");
            } else if ("DRIVER_NEGOTIATED".equals(endpoint.getAuthMode())) {
                if (endpoint.getCredentialId() == null) return blocked("LOCATOR_INVALID");
                var credential =
                        batchLoaded
                                ? preloadedCredential
                                : credentials.selectById(endpoint.getCredentialId());
                if (credential == null
                        || !source.getId().equals(credential.getSourceId())
                        || !Objects.equals(endpoint.getPurpose(), credential.getPurpose()))
                    return blocked("LOCATOR_INVALID");
                credentialPurpose = credential.getPurpose();
                var secret =
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
            } else return blocked("LOCATOR_INVALID");
            String host;
            Integer port;
            if (!direct) {
                host = endpoint.getHost();
                port = source.getRtspPort() == null ? 554 : source.getRtspPort();
            } else if ("SOURCE".equals(locator.getRtspHostMode())) {
                host = endpoint.getHost();
                port = endpoint.getPort();
            } else if ("EXPLICIT".equals(locator.getRtspHostMode())) {
                host = locator.getRtspHost();
                port = locator.getRtspPort();
            } else return blocked("LOCATOR_INVALID");
            try {
                host = rules.host(host, source.getNetworkPolicyKey());
                CameraSourceRules.port(port);
            } catch (BusinessException ex) {
                return blocked("NETWORK_BLOCKED");
            }
            CameraAccessContext context = null;
            if (!direct) {
                CameraSourceRules.port(endpoint.getPort());
                context =
                        new CameraAccessContext(
                                URI.create(
                                        endpoint.getScheme()
                                                + "://"
                                                + (host.contains(":") ? "[" + host + "]" : host)
                                                + ":"
                                                + endpoint.getPort()
                                                + (endpoint.getBasePath() == null
                                                                || endpoint.getBasePath().isEmpty()
                                                        ? "/"
                                                        : endpoint.getBasePath())),
                                source.getNetworkPolicyKey(),
                                username,
                                password,
                                port,
                                authorizationCheck,
                                0);
            }
            String path = config.path("encodedPath").asText();
            String query = config.path("rawQuery").asText();
            if (direct
                    && (!path.startsWith("/")
                            || path.contains("?")
                            || path.contains("#")
                            || query.startsWith("?")
                            || query.contains("#"))) return blocked("LOCATOR_INVALID");
            if (platform)
                com.streamfusion.platform.camera.access.adapter.HikPlatformApi.requireCode(
                        config.path("channelKey").asText());
            URI uri =
                    platform
                            ? null
                            : direct
                                    ? URI.create(
                                            "rtsp://"
                                                    + (host.contains(":") ? "[" + host + "]" : host)
                                                    + ":"
                                                    + port
                                                    + path
                                                    + (query.isEmpty() ? "" : "?" + query))
                                    : streams.configuredUri(locator, config, context);
            boolean stream = CameraStreamProtocol.stream(locator.getLocatorKind());
            if (!platform && !stream) CameraStatusStreamResolver.validateRtsp(uri);
            if (direct
                    && (!path.equals(uri.getRawPath())
                            || !Objects.equals(query.isEmpty() ? null : query, uri.getRawQuery())
                            || uri.getRawUserInfo() != null
                            || uri.getRawFragment() != null
                            || uri.toASCIIString().length() > 8192))
                return blocked("LOCATOR_INVALID");
            var semantic = new LinkedHashMap<String, Object>();
            semantic.put("cameraId", camera.getId().toString());
            semantic.put("profileId", profile.getId().toString());
            semantic.put("sourceId", source.getId().toString());
            semantic.put("sourceEnabled", source.getEnabled());
            semantic.put("cameraLifecycle", camera.getLifecycle());
            semantic.put("profileEnabled", profile.getEnabled());
            semantic.put("sourceAdapter", source.getAdapterType());
            semantic.put("hostMode", locator.getRtspHostMode());
            semantic.put("host", host);
            semantic.put("port", port);
            semantic.put("transport", locator.getRtspTransport());
            semantic.put("encodedPath", path);
            semantic.put("rawQuery", query);
            semantic.put("endpointPurpose", endpoint.getPurpose());
            semantic.put("authMode", endpoint.getAuthMode());
            semantic.put("credentialPurpose", credentialPurpose);
            semantic.put("username", username);
            semantic.put("password", password);
            semantic.put("networkPolicyKey", source.getNetworkPolicyKey());
            semantic.put(
                    "networkPolicy",
                    properties.getNetworkPolicies().get(source.getNetworkPolicyKey()));
            if (!direct) {
                semantic.put("locatorKind", locator.getLocatorKind());
                semantic.put("protocolConfig", config);
                semantic.put("controlEndpoint", context.endpoint().toASCIIString());
            }
            byte[] digest = crypto.digest(keyId, "camera-status-connection", semantic);
            String blocking =
                    !Boolean.TRUE.equals(source.getEnabled())
                            ? "SOURCE_DISABLED"
                            : !"ENABLED".equals(camera.getLifecycle())
                                    ? "CHANNEL_DISABLED"
                                    : !Boolean.TRUE.equals(profile.getEnabled())
                                            ? "PROFILE_DISABLED"
                                            : null;
            return new Inspection(
                    platform || stream
                            ? null
                            : new RtspDescribeProbe.Target(
                                    uri,
                                    source.getNetworkPolicyKey(),
                                    username,
                                    password,
                                    authorizationCheck),
                    digest,
                    blocking,
                    direct || platform ? null : streams.deferred(locator, config, context));
        } catch (CameraAdapterException ex) {
            return blocked(ex.reasonCode());
        } catch (PreviewFailure ex) {
            return blocked(ex.reason());
        } catch (BusinessException ex) {
            return blocked("CONFIGURATION_UNAVAILABLE");
        } catch (IllegalArgumentException ex) {
            return blocked("LOCATOR_INVALID");
        }
    }

    private static Inspection blocked(String reason) {
        return new Inspection(null, null, reason);
    }

    public static void requireRunnable(Inspection inspection) {
        if (inspection.blockingReasonCode() != null)
            throw new CameraAdapterException(
                    "CONFIGURATION_UNAVAILABLE".equals(inspection.blockingReasonCode())
                            ? "CONFIGURATION_UNAVAILABLE"
                            : "CONFIGURATION_CHANGED");
    }
}
