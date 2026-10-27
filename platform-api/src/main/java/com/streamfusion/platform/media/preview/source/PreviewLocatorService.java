package com.streamfusion.platform.media.preview.source;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
import com.streamfusion.platform.camera.ingress.CameraIngressService;
import com.streamfusion.platform.camera.mapper.CameraCredentialMapper;
import com.streamfusion.platform.camera.mapper.CameraEndpointMapper;
import com.streamfusion.platform.camera.mapper.CameraLocatorMapper;
import com.streamfusion.platform.camera.mapper.CameraSourceMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraEndpointEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.media.preview.PreviewDeadline;
import com.streamfusion.platform.media.preview.PreviewFailure;
import jakarta.annotation.PreDestroy;
import java.net.InetAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import org.springframework.stereotype.Service;

@Service
public class PreviewLocatorService {
    private final CameraLocatorMapper locators;
    private final CameraSourceMapper sources;
    private final CameraEndpointMapper endpoints;
    private final CameraCredentialMapper credentials;
    private final CameraCryptoService crypto;
    private final CameraSourceRules rules;
    private final StreamLocatorAdapterRegistry adapters;
    private final CameraIngressService ingress;
    private final ThreadPoolExecutor dns =
            new ThreadPoolExecutor(
                    2,
                    2,
                    0,
                    TimeUnit.SECONDS,
                    new ArrayBlockingQueue<>(16),
                    runnable -> {
                        var t = new Thread(runnable, "preview-dns");
                        t.setDaemon(true);
                        return t;
                    },
                    new ThreadPoolExecutor.AbortPolicy());

    public PreviewLocatorService(
            CameraLocatorMapper locators,
            CameraSourceMapper sources,
            CameraEndpointMapper endpoints,
            CameraCredentialMapper credentials,
            CameraCryptoService crypto,
            CameraSourceRules rules,
            StreamLocatorAdapterRegistry adapters) {
        this(locators, sources, endpoints, credentials, crypto, rules, adapters, null);
    }

    @org.springframework.beans.factory.annotation.Autowired
    public PreviewLocatorService(
            CameraLocatorMapper locators,
            CameraSourceMapper sources,
            CameraEndpointMapper endpoints,
            CameraCredentialMapper credentials,
            CameraCryptoService crypto,
            CameraSourceRules rules,
            StreamLocatorAdapterRegistry adapters,
            CameraIngressService ingress) {
        this.locators = locators;
        this.sources = sources;
        this.endpoints = endpoints;
        this.credentials = credentials;
        this.crypto = crypto;
        this.rules = rules;
        this.adapters = adapters;
        this.ingress = ingress;
    }

    @PreDestroy
    public void close() {
        dns.shutdownNow();
    }

    /**
     * Contains credentials in memory only. Avoid default record toString and public serialization.
     */
    public record Resolved(URI uri) {
        @Override
        public String toString() {
            return "Resolved[redacted]";
        }
    }

    /** Stable local configuration identity, not an expiring vendor playback URL. */
    public String connectionKey(CameraProfileEntity profile) {
        var locator = locators.selectById(profile.getId());
        var source = sources.selectById(profile.getSourceId());
        if (locator == null || source == null || !Boolean.TRUE.equals(source.getEnabled()))
            throw new PreviewFailure("LOCATOR_NOT_CONFIGURED");
        if (!profile.getSourceId().equals(locator.getSourceId()))
            throw new PreviewFailure("LOCATOR_INVALID");
        var endpoint =
                endpoints.selectOne(
                        new LambdaQueryWrapper<CameraEndpointEntity>()
                                .eq(CameraEndpointEntity::getSourceId, profile.getSourceId())
                                .eq(
                                        CameraEndpointEntity::getPurpose,
                                        locator.getEndpointPurpose()));
        if (endpoint == null) throw new PreviewFailure("LOCATOR_NOT_CONFIGURED");
        var snapshot = new java.util.LinkedHashMap<String, Object>();
        snapshot.put("profileId", profile.getId());
        snapshot.put("sourceId", profile.getSourceId());
        snapshot.put("channelId", profile.getChannelId());
        snapshot.put("profileVersion", profile.getVersion());
        snapshot.put("sourceVersion", source.getVersion());
        snapshot.put("networkPolicy", source.getNetworkPolicyKey());
        snapshot.put("rtspPort", source.getRtspPort());
        snapshot.put("locator", locator);
        snapshot.put("endpoint", endpoint);
        if (endpoint.getCredentialId() != null) {
            var credential = credentials.selectById(endpoint.getCredentialId());
            if (credential == null || !source.getId().equals(credential.getSourceId()))
                throw new PreviewFailure("LOCATOR_INVALID");
            snapshot.put("credential", credential);
        }
        // Only the HMAC is retained. Neither plaintext credentials nor vendor URLs are cached.
        return crypto.sign("preview-connection", snapshot);
    }

    public Resolved resolve(CameraProfileEntity profile, Runnable authorization) {
        return resolve(profile, authorization, PreviewDeadline.after(Duration.ofSeconds(10)));
    }

    public Resolved resolve(
            CameraProfileEntity profile, Runnable authorization, PreviewDeadline deadline) {
        deadline.check();
        authorization.run();
        var locator = locators.selectById(profile.getId());
        var source = sources.selectById(profile.getSourceId());
        if (locator == null || source == null || !Boolean.TRUE.equals(source.getEnabled()))
            throw new PreviewFailure("LOCATOR_NOT_CONFIGURED");
        if (!profile.getSourceId().equals(locator.getSourceId()))
            throw new PreviewFailure("LOCATOR_INVALID");
        var adapter = adapters.required(locator.getLocatorKind());
        var endpoint =
                endpoints.selectOne(
                        new LambdaQueryWrapper<CameraEndpointEntity>()
                                .eq(CameraEndpointEntity::getSourceId, profile.getSourceId())
                                .eq(
                                        CameraEndpointEntity::getPurpose,
                                        locator.getEndpointPurpose()));
        if (endpoint == null) throw new PreviewFailure("LOCATOR_NOT_CONFIGURED");
        String username = null, password = null;
        if (endpoint.getCredentialId() != null) {
            var credential = credentials.selectById(endpoint.getCredentialId());
            if (credential == null || !source.getId().equals(credential.getSourceId()))
                throw new PreviewFailure("LOCATOR_INVALID");
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
                                deadline.check();
                                authorization.run();
                            },
                            deadline.deadlineNanos());
            boolean rtsp = "RTSP".equals(locator.getLocatorKind());
            JsonNode config =
                    crypto.decrypt(
                            "camera_profile_locator/"
                                    + locator.getSourceId()
                                    + "/"
                                    + locator.getProfileId()
                                    + "/"
                                    + locator.getLocatorKind(),
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
            deadline.check();
            if (uri == null
                    || !(CameraStreamProtocol.supports(locator.getLocatorKind())
                            ? CameraStreamProtocol.valueOf(locator.getLocatorKind())
                                    .accepts(uri.getScheme())
                            : "rtsp".equalsIgnoreCase(uri.getScheme()))
                    || uri.getRawUserInfo() != null
                    || uri.getFragment() != null
                    || uri.getHost() == null
                    || uri.toString().length() > 8192) throw new PreviewFailure("LOCATOR_INVALID");
            if (CameraStreamProtocol.ingress(locator.getLocatorKind())) {
                if (ingress == null) throw new PreviewFailure("INGRESS_NOT_CONFIGURED");
                uri = ingress.resolve(locator.getLocatorKind(), uri, authorization, deadline);
                username = null;
                password = null;
            }
            String pinned =
                    pin(
                            uri.getHost().replace("[", "").replace("]", ""),
                            source.getNetworkPolicyKey(),
                            deadline);
            int port =
                    uri.getPort() < 0
                            ? ("https".equals(uri.getScheme())
                                    ? 443
                                    : "http".equals(uri.getScheme())
                                            ? 80
                                            : "rtmp".equals(uri.getScheme()) ? 1935 : 554)
                            : uri.getPort();
            CameraSourceRules.port(port);
            String authority =
                    ("HIK_PLATFORM".equals(locator.getLocatorKind())
                                    || username == null
                                    || username.isEmpty())
                            ? ""
                            : encode(username)
                                    + ":"
                                    + encode(password == null ? "" : password)
                                    + "@";
            authority += (pinned.contains(":") ? "[" + pinned + "]" : pinned) + ":" + port;
            URI authenticated =
                    URI.create(
                            uri.getScheme()
                                    + "://"
                                    + authority
                                    + uri.getRawPath()
                                    + (uri.getRawQuery() == null ? "" : "?" + uri.getRawQuery())
                                    + PinnedMediaUri.fragment(uri));
            authorization.run();
            deadline.check();
            return new Resolved(authenticated);
        } catch (CameraAdapterException ex) {
            deadline.check();
            throw new PreviewFailure(ex.reasonCode());
        } catch (java.net.URISyntaxException | IllegalArgumentException ex) {
            throw new PreviewFailure("LOCATOR_INVALID");
        }
    }

    private String pin(String host, String policy, PreviewDeadline deadline) {
        deadline.check();
        allowedHost(host, policy);
        java.util.concurrent.Future<InetAddress[]> job;
        try {
            job = dns.submit(() -> InetAddress.getAllByName(host));
        } catch (java.util.concurrent.RejectedExecutionException ex) {
            throw new PreviewFailure("DNS_UNAVAILABLE");
        }
        try {
            var addresses =
                    job.get(
                            Math.min(TimeUnit.SECONDS.toNanos(3), deadline.remaining().toNanos()),
                            TimeUnit.NANOSECONDS);
            deadline.check();
            if (addresses.length == 0 || addresses.length > 16)
                throw new PreviewFailure("DESTINATION_REJECTED");
            for (var address : addresses) allowedHost(address.getHostAddress(), policy);
            return addresses[0].getHostAddress();
        } catch (InterruptedException ex) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (java.util.concurrent.ExecutionException
                | java.util.concurrent.TimeoutException ex) {
            deadline.check();
            throw new PreviewFailure("DNS_UNAVAILABLE");
        } finally {
            job.cancel(true);
        }
    }

    private void allowedHost(String host, String policy) {
        try {
            rules.host(host, policy);
        } catch (BusinessException ex) {
            throw new PreviewFailure("DESTINATION_REJECTED");
        }
    }

    private static String encode(String value) {
        return java.net.URLEncoder.encode(value, StandardCharsets.UTF_8).replace("+", "%20");
    }
}
