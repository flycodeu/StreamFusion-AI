package com.streamfusion.platform.camera.service;

import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.access.adapter.CameraAccessCatalog;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
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
    private final CameraAccessAdapterRegistry adapters;

    @Transactional(propagation = Propagation.MANDATORY)
    public void create(long sourceId, long profileId, String externalKey, CameraLocatorDto input) {
        save(sourceId, profileId, externalKey, input, null);
    }

    @Transactional(propagation = Propagation.MANDATORY)
    public void update(long sourceId, long profileId, String externalKey, CameraLocatorDto input) {
        var current = locators.selectById(profileId);
        if (current == null || current.getSourceId() != sourceId)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (CameraStreamProtocol.stream(current.getLocatorKind())) {
            updateStream(sourceId, input, current);
            return;
        }
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
        summary.put("editable", CameraStreamProtocol.supports(locator.getLocatorKind()));
        if (!"RTSP".equals(locator.getLocatorKind())) return summary;
        summary.put("hostMode", locator.getRtspHostMode());
        summary.put("transport", locator.getRtspTransport());
        summary.put("host", locator.getRtspHost());
        summary.put("port", locator.getRtspPort());
        if (admin) {
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
        if (CameraStreamProtocol.stream(locator.kind())) {
            var uri =
                    CameraStreamProtocol.valueOf(locator.kind())
                            .parse(locator.uri().toASCIIString());
            if (uri.getRawUserInfo() != null) throw CameraSourceRules.invalid();
            config.put("uri", uri.toASCIIString());
        } else config.putAll(adapters.locatorConfiguration(locator));
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

    /** URL remains write-only. A source groups URLs on one immutable origin. */
    private void updateStream(long sourceId, CameraLocatorDto input, CameraLocatorEntity current) {
        if (input == null
                || input.fullUrl() == null
                || !"TCP".equals(input.transport())
                || input.hostMode() != null
                || input.host() != null
                || input.port() != null
                || input.pathSecret() != null
                || input.querySecret() != null) throw CameraSourceRules.invalid();
        var source = sources.requireSource(sourceId);
        var protocol = CameraStreamProtocol.valueOf(current.getLocatorKind());
        var uri = protocol.parse(input.fullUrl());
        if (!current.getLocatorKind().equals(source.getAdapterType())
                || uri.getRawUserInfo() != null) throw CameraSourceRules.invalid();
        var old =
                crypto.decrypt(
                        "camera_profile_locator/"
                                + sourceId
                                + "/"
                                + current.getProfileId()
                                + "/"
                                + current.getLocatorKind(),
                        new CameraCryptoService.Envelope(
                                current.getProtocolEncryptionKeyId(),
                                current.getProtocolSecretNonce(),
                                current.getProtocolSecretCiphertext(),
                                current.getProtocolSecretTag()));
        var previous = protocol.parse(old.path("uri").asText());
        if (!uri.getHost().equalsIgnoreCase(previous.getHost())
                || protocol.port(uri) != protocol.port(previous)
                || !uri.getScheme().equalsIgnoreCase(previous.getScheme()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        var config = new LinkedHashMap<String, Object>();
        old.fields().forEachRemaining(e -> config.put(e.getKey(), e.getValue()));
        config.put("uri", uri.toASCIIString());
        var encrypted =
                crypto.encrypt(
                        "camera_profile_locator/"
                                + sourceId
                                + "/"
                                + current.getProfileId()
                                + "/"
                                + current.getLocatorKind(),
                        config,
                        16000);
        current.setProtocolSecretCiphertext(encrypted.ciphertext());
        current.setProtocolSecretNonce(encrypted.nonce());
        current.setProtocolSecretTag(encrypted.tag());
        current.setProtocolEncryptionKeyId(encrypted.keyId());
        current.setUpdatedAt(now());
        locators.updateById(current);
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
            var uri = CameraStreamProtocol.RTSP.parse(input.fullUrl());
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
        if (!"RTSP".equals(source.getAdapterType())) throw CameraSourceRules.invalid();
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
