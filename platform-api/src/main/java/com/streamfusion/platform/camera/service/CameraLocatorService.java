package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.camera.access.adapter.CameraAccessCatalog;
import com.streamfusion.platform.camera.mapper.CameraLocatorMapper;
import com.streamfusion.platform.camera.pojo.dto.CameraLocatorDto;
import com.streamfusion.platform.camera.pojo.dto.SecretWrite;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.LinkedHashMap;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Propagation;
import org.springframework.transaction.annotation.Transactional;

/** Persistent RTSP locators. Public summaries never include even an encoded path or query. */
@Service
@RequiredArgsConstructor
public class CameraLocatorService {
    private final CameraLocatorMapper locators;
    private final com.streamfusion.platform.camera.mapper.CameraSourceMapper sourceRows;
    private final CameraSourceService sources;
    private final CameraSourceRules rules;
    private final CameraCryptoService crypto;
    private final Clock clock;

    @Transactional(propagation = Propagation.MANDATORY)
    public void create(long sourceId, long profileId, String externalKey, CameraLocatorDto input) {
        save(sourceId, profileId, externalKey, input, null);
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public void update(long sourceId, long profileId, String externalKey, CameraLocatorDto input) {
        var current = locators.selectById(profileId);
        if (current == null || current.getSourceId() != sourceId)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (!"RTSP".equals(current.getLocatorKind()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        save(sourceId, profileId, externalKey, input, current);
    }

    public boolean valid(long profileId) {
        var locator = locators.selectById(profileId);
        if (locator == null) return false;
        byte[] ciphertext =
                "RTSP".equals(locator.getLocatorKind())
                        ? locator.getRtspSecretCiphertext()
                        : locator.getProtocolSecretCiphertext();
        if (!sources.supportsAdapter(locator.getLocatorKind())
                || ciphertext == null
                || ciphertext.length == 0) return false;
        var source = sourceRows.selectById(locator.getSourceId());
        return source != null && Boolean.TRUE.equals(source.getEnabled());
    }

    public Map<String, Object> summary(long profileId, boolean admin) {
        var locator = locators.selectById(profileId);
        if (locator == null) return Map.of("configured", false);
        var summary = new LinkedHashMap<String, Object>();
        summary.put("locatorKind", locator.getLocatorKind());
        summary.put("configured", true);
        summary.put("editable", "RTSP".equals(locator.getLocatorKind()));
        if (!"RTSP".equals(locator.getLocatorKind())) return summary;
        summary.put("hostMode", locator.getRtspHostMode());
        summary.put("transport", locator.getRtspTransport());
        if (admin) {
            summary.put("host", locator.getRtspHost());
            summary.put("port", locator.getRtspPort());
            summary.put("pathConfigured", true);
            // A secret stays write-only. No query-value or comparable hash is returned.
            var plain = crypto.decrypt(aad(locator), envelope(locator));
            summary.put("queryConfigured", !plain.path("rawQuery").asText().isEmpty());
        }
        return summary;
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public void delete(long profileId) {
        locators.deleteById(profileId);
    }

    /** Protocol identity is encrypted; a resolved temporary URI is deliberately never persisted. */
    @Transactional(propagation = Propagation.MANDATORY)
    public void createImported(
            long sourceId,
            long profileId,
            String channelKey,
            String profileKey,
            CameraAccessCatalog.Locator locator,
            int rtspPort) {
        if (locator == null || !sources.supportsAdapter(locator.kind()))
            throw CameraSourceRules.invalid();
        String identity =
                crypto.canonical(Map.of("channelKey", channelKey, "profileKey", profileKey));
        if ("RTSP".equals(locator.kind())) {
            var uri = locator.uri();
            if (uri == null
                    || !"rtsp".equalsIgnoreCase(uri.getScheme())
                    || uri.getHost() == null
                    || uri.getRawFragment() != null
                    || uri.getRawUserInfo() != null) throw CameraSourceRules.invalid();
            create(
                    sourceId,
                    profileId,
                    identity,
                    new CameraLocatorDto(
                            "EXPLICIT",
                            uri.getHost().replace("[", "").replace("]", ""),
                            uri.getPort() < 0 ? 554 : uri.getPort(),
                            new SecretWrite(
                                    "REPLACE",
                                    uri.getRawPath() == null || uri.getRawPath().isEmpty()
                                            ? "/"
                                            : uri.getRawPath()),
                            new SecretWrite(
                                    "REPLACE", uri.getRawQuery() == null ? "" : uri.getRawQuery()),
                            "TCP"));
            return;
        }
        var source = sources.requireSource(sourceId);
        if (!locator.kind().equals(source.getAdapterType())
                || !Boolean.TRUE.equals(source.getEnabled()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        CameraSourceRules.port(rtspPort);
        Map<String, Object> config = new LinkedHashMap<>();
        config.put("channelKey", channelKey);
        config.put("profileKey", profileKey);
        if ("ONVIF".equals(locator.kind()) || "HIKVISION".equals(locator.kind())) {
            config.put("profileToken", opaque(locator.profileToken(), 512));
            if ("ONVIF".equals(locator.kind())) {
                if (locator.serviceNamespace() != null || locator.serviceEndpoint() != null) {
                    if (!("http://www.onvif.org/ver10/media/wsdl".equals(locator.serviceNamespace())
                                    || "http://www.onvif.org/ver20/media/wsdl"
                                            .equals(locator.serviceNamespace()))
                            || locator.serviceEndpoint() == null) throw CameraSourceRules.invalid();
                    config.put("serviceNamespace", locator.serviceNamespace());
                    config.put("serviceEndpoint", locator.serviceEndpoint().toASCIIString());
                }
            }
        } else {
            if (locator.streamType() == null
                    || locator.streamType() < 0
                    || locator.streamType() > 255) throw CameraSourceRules.invalid();
            config.put("streamType", locator.streamType());
            // Platform monitor key or native channel/stream reference, never a URL.
            if (locator.profileToken() != null)
                config.put("resourceToken", opaque(locator.profileToken(), 512));
        }
        var row = new CameraLocatorEntity();
        row.setProfileId(profileId);
        row.setSourceId(sourceId);
        row.setLocatorKind(locator.kind());
        row.setEndpointPurpose(sources.endpointPurpose(sourceId));
        row.setIdentityDigest(identityDigest(sourceId, identity));
        var encrypted =
                crypto.encrypt(
                        "camera_profile_locator/"
                                + sourceId
                                + "/"
                                + profileId
                                + "/"
                                + locator.kind(),
                        config,
                        16000);
        row.setProtocolSecretCiphertext(encrypted.ciphertext());
        row.setProtocolSecretNonce(encrypted.nonce());
        row.setProtocolSecretTag(encrypted.tag());
        row.setProtocolEncryptionKeyId(encrypted.keyId());
        row.setCreatedAt(now());
        row.setUpdatedAt(row.getCreatedAt());
        locators.insert(row);
    }

    private static String opaque(String value, int max) {
        if (value == null
                || value.isEmpty()
                || value.codePointCount(0, value.length()) > max
                || value.codePoints().anyMatch(Character::isISOControl))
            throw CameraSourceRules.invalid();
        return value;
    }

    private byte[] identityDigest(long sourceId, String externalKey) {
        try {
            return MessageDigest.getInstance("SHA-256")
                    .digest(
                            crypto.canonical(
                                            Map.of(
                                                    "sourceId",
                                                    Long.toString(sourceId),
                                                    "externalKey",
                                                    externalKey))
                                    .getBytes(StandardCharsets.UTF_8));
        } catch (java.security.NoSuchAlgorithmException ex) {
            throw new IllegalStateException("SHA-256 unavailable");
        }
    }

    private void save(
            long sourceId,
            long profileId,
            String externalKey,
            CameraLocatorDto input,
            CameraLocatorEntity current) {
        if (input == null || !"TCP".equals(input.transport())) throw CameraSourceRules.invalid();
        if (input.fullUrl() != null) {
            if (input.hostMode() != null
                    || input.host() != null
                    || input.port() != null
                    || input.pathSecret() != null
                    || input.querySecret() != null) throw CameraSourceRules.invalid();
            var uri =
                    com.streamfusion.platform.camera.access.service.CameraConnectionRules.parseRtsp(
                            input.fullUrl());
            if (uri.getRawUserInfo() != null) throw CameraSourceRules.invalid();
            input =
                    new CameraLocatorDto(
                            "EXPLICIT",
                            uri.getHost().replace("[", "").replace("]", ""),
                            uri.getPort() < 0 ? 554 : uri.getPort(),
                            new SecretWrite("REPLACE", uri.getRawPath()),
                            new SecretWrite(
                                    "REPLACE", uri.getRawQuery() == null ? "" : uri.getRawQuery()),
                            "TCP");
        }
        var source = sources.requireManualSource(sourceId, null);
        String host = null;
        Integer port = null;
        if ("SOURCE".equals(input.hostMode())) {
            if (input.host() != null || input.port() != null) throw CameraSourceRules.invalid();
        } else if ("EXPLICIT".equals(input.hostMode())) {
            host = rules.host(input.host(), source.getNetworkPolicyKey());
            CameraSourceRules.port(input.port());
            port = input.port();
        } else throw CameraSourceRules.invalid();
        String oldPath = null, oldQuery = null;
        if (current != null) {
            var plain = crypto.decrypt(aad(current), envelope(current));
            oldPath = plain.path("encodedPath").asText();
            oldQuery = plain.path("rawQuery").asText();
        }
        String path = CameraSourceRules.secret(input.pathSecret(), oldPath, 2048, false);
        String query = CameraSourceRules.secret(input.querySecret(), oldQuery, 4096, true);
        // Validate URI structure without decoding/re-encoding; preserve escapes byte for byte.
        if (!path.startsWith("/")
                || path.contains("?")
                || path.contains("#")
                || query.startsWith("?")
                || query.contains("#")) throw CameraSourceRules.invalid();
        try {
            var uri =
                    new java.net.URI(
                            "rtsp://validation.invalid"
                                    + path
                                    + (query.isEmpty() ? "" : "?" + query));
            if (!path.equals(uri.getRawPath())
                    || !java.util.Objects.equals(query.isEmpty() ? null : query, uri.getRawQuery()))
                throw CameraSourceRules.invalid();
        } catch (java.net.URISyntaxException ex) {
            throw CameraSourceRules.invalid();
        }
        boolean fresh = current == null;
        if (fresh) {
            current = new CameraLocatorEntity();
            current.setProfileId(profileId);
            current.setSourceId(sourceId);
            current.setLocatorKind("RTSP");
            current.setEndpointPurpose("RTSP");
            current.setCreatedAt(now());
            current.setIdentityDigest(identityDigest(sourceId, externalKey));
        }
        var encrypted =
                crypto.encrypt(aad(current), Map.of("encodedPath", path, "rawQuery", query), 16000);
        current.setRtspHostMode(input.hostMode());
        current.setRtspHost(host);
        current.setRtspPort(port);
        current.setRtspTransport("TCP");
        current.setRtspSecretCiphertext(encrypted.ciphertext());
        current.setRtspSecretNonce(encrypted.nonce());
        current.setRtspSecretTag(encrypted.tag());
        current.setRtspEncryptionKeyId(encrypted.keyId());
        current.setUpdatedAt(now());
        if (fresh) locators.insert(current);
        else
            locators.update(
                    null,
                    new LambdaUpdateWrapper<CameraLocatorEntity>()
                            .eq(CameraLocatorEntity::getProfileId, profileId)
                            .set(CameraLocatorEntity::getRtspHostMode, input.hostMode())
                            .set(CameraLocatorEntity::getRtspHost, host)
                            .set(CameraLocatorEntity::getRtspPort, port)
                            .set(
                                    CameraLocatorEntity::getRtspSecretCiphertext,
                                    encrypted.ciphertext())
                            .set(CameraLocatorEntity::getRtspSecretNonce, encrypted.nonce())
                            .set(CameraLocatorEntity::getRtspSecretTag, encrypted.tag())
                            .set(CameraLocatorEntity::getRtspEncryptionKeyId, encrypted.keyId())
                            .set(CameraLocatorEntity::getUpdatedAt, current.getUpdatedAt()));
    }

    private static String aad(CameraLocatorEntity l) {
        return "camera_profile_locator/" + l.getSourceId() + "/" + l.getProfileId() + "/RTSP";
    }

    private static CameraCryptoService.Envelope envelope(CameraLocatorEntity l) {
        return new CameraCryptoService.Envelope(
                l.getRtspEncryptionKeyId(),
                l.getRtspSecretNonce(),
                l.getRtspSecretCiphertext(),
                l.getRtspSecretTag());
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"));
    }
}
