package com.streamfusion.platform.camera.access.service;

import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.access.adapter.CameraAccessCatalog;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
import com.streamfusion.platform.camera.access.pojo.CameraConnection;
import com.streamfusion.platform.camera.config.CameraProperties;
import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** Normalizes the user-facing connection, not arbitrary internal endpoint/profile objects. */
@Component
@RequiredArgsConstructor
public class CameraConnectionRules {
    private final CameraProperties properties;
    private final CameraAccessAdapterRegistry adapters;
    private final CameraSourceRules rules;

    public CameraConnection normalize(CameraConnection input) {
        if (input == null || input.method() == null || !adapters.methods().contains(input.method()))
            throw CameraSourceRules.invalid();
        int page = input.pageNumber() == null ? 1 : input.pageNumber();
        int size = input.pageSize() == null ? 100 : input.pageSize();
        boolean paged =
                !"AUTO".equals(input.method()) && adapters.descriptor(input.method()).paged();
        boolean platform =
                !"AUTO".equals(input.method())
                        && adapters.supportsCategory(input.method(), "PLATFORM");
        if (page < 1
                || page > 100000
                || size < 1
                || size > 100
                || (!paged && (page != 1 || size != 100))) throw CameraSourceRules.invalid();
        if (input.regionIndexCode() != null) {
            if (!platform) throw CameraSourceRules.invalid();
            adapters.platform(input.method()).validateRegionCode(input.regionIndexCode());
        }
        String policy = properties.defaultNetworkPolicyKey(input.networkPolicyKey());
        rules.policy(policy);
        String username = secret(input.username(), 128), password = secret(input.password(), 512);
        String name = CameraSourceRules.text(input.name(), 100, false);
        int rtspPort = input.rtspPort() == null ? 554 : input.rtspPort();
        CameraSourceRules.port(rtspPort);
        if (CameraStreamProtocol.supports(input.method())) {
            boolean rtsp = "RTSP".equals(input.method());
            var protocol = CameraStreamProtocol.valueOf(input.method());
            if (rtsp ? input.streamUrls() != null : input.rtspUrls() != null)
                throw CameraSourceRules.invalid();
            if (name == null
                    || name.isBlank()
                    || input.urls() == null
                    || input.urls().isEmpty()
                    || input.urls().size() > 8) throw CameraSourceRules.invalid();
            var urls = new ArrayList<String>();
            String host = null;
            int port = 554;
            String scheme = null;
            for (String text : input.urls()) {
                URI uri = protocol.parse(text);
                String nextHost = rules.host(unbracket(uri.getHost()), policy);
                int nextPort = protocol.port(uri);
                CameraSourceRules.port(nextPort);
                if (host != null
                        && (!host.equals(nextHost)
                                || port != nextPort
                                || !scheme.equalsIgnoreCase(uri.getScheme())))
                    throw CameraSourceRules.invalid();
                host = nextHost;
                port = nextPort;
                scheme = uri.getScheme().toLowerCase(java.util.Locale.ROOT);
                if (uri.getRawUserInfo() != null) {
                    String[] pair = uri.getRawUserInfo().split(":", 2);
                    if (pair.length != 2) throw CameraSourceRules.invalid();
                    String u = secret(decode(pair[0]), 128), p = secret(decode(pair[1]), 512);
                    if ((username != null && !username.equals(u))
                            || (password != null && !password.equals(p)))
                        throw CameraSourceRules.invalid();
                    username = u;
                    password = p;
                }
                // Do not decode/re-encode a signed path or query.
                String clean =
                        scheme
                                + "://"
                                + authority(host, port)
                                + uri.getRawPath()
                                + (uri.getRawQuery() == null ? "" : "?" + uri.getRawQuery());
                if (urls.contains(clean)) throw CameraSourceRules.invalid();
                urls.add(clean);
            }
            credentials(username, password);
            if (CameraStreamProtocol.ingress(input.method()) && username != null)
                throw CameraSourceRules.invalid();
            if (input.sourceId() != null
                    && (!Objects.equals(host, input.host()) || !Objects.equals(port, input.port())))
                throw CameraSourceRules.invalid();
            return new CameraConnection(
                    input.method(),
                    name,
                    host,
                    port,
                    scheme,
                    username,
                    password,
                    rtspPort,
                    policy,
                    input.sourceId(),
                    input.sourceVersion(),
                    rtsp ? List.copyOf(urls) : null,
                    page,
                    size,
                    null,
                    null,
                    rtsp ? null : List.copyOf(urls));
        }
        if (input.streamUrls() != null) throw CameraSourceRules.invalid();
        if (input.rtspUrls() != null && !input.rtspUrls().isEmpty())
            throw CameraSourceRules.invalid();
        String scheme = input.scheme() == null ? (platform ? "https" : "http") : input.scheme();
        if (!List.of("http", "https").contains(scheme)) throw CameraSourceRules.invalid();
        String host = rules.host(input.host(), policy);
        int port = input.port() == null ? ("https".equals(scheme) ? 443 : 80) : input.port();
        CameraSourceRules.port(port);
        credentials(username, password);
        adapters.validateCredentials(input.method(), username, password);
        return new CameraConnection(
                input.method(),
                name == null || name.isBlank() ? host : name,
                host,
                port,
                scheme,
                username,
                password,
                rtspPort,
                policy,
                input.sourceId(),
                input.sourceVersion(),
                null,
                page,
                size,
                input.regionIndexCode(),
                input.includeSubRegions());
    }

    public URI endpoint(CameraConnection connection) {
        return URI.create(
                connection.scheme()
                        + "://"
                        + authority(connection.host(), connection.port())
                        + ("AUTO".equals(connection.method())
                                ? ""
                                : adapters.descriptor(connection.method()).endpointPath()));
    }

    public CameraAccessCatalog streamCatalog(CameraConnection connection) {
        var profiles = new ArrayList<CameraAccessCatalog.Profile>();
        for (int i = 0; i < connection.urls().size(); i++) {
            profiles.add(
                    new CameraAccessCatalog.Profile(
                            // Keep persisted identity keys compatible with existing imports.
                            "rtsp-profile:" + stableKey(connection.urls().get(i)),
                            "码流 " + (i + 1),
                            "UNKNOWN",
                            null,
                            null,
                            null,
                            null,
                            null,
                            new CameraAccessCatalog.Locator(
                                    connection.method(),
                                    URI.create(connection.urls().get(i)),
                                    null,
                                    null)));
        }
        return new CameraAccessCatalog(
                connection.method(),
                null,
                List.of(
                        new CameraAccessCatalog.Channel(
                                "rtsp-config:"
                                        + stableKey(
                                                String.join(
                                                        "\n",
                                                        connection.urls().stream()
                                                                .sorted()
                                                                .toList())),
                                connection.name(),
                                profiles,
                                false)),
                true,
                List.of(
                        "RTSP".equals(connection.method())
                                ? "RTSP_CONFIGURATION_ONLY"
                                : "STREAM_CONFIGURATION_ONLY"));
    }

    private static String stableKey(String value) {
        try {
            return java.util.HexFormat.of()
                    .formatHex(
                            java.security.MessageDigest.getInstance("SHA-256")
                                    .digest(value.getBytes(StandardCharsets.UTF_8)));
        } catch (java.security.NoSuchAlgorithmException ex) {
            throw new IllegalStateException("Required digest unavailable");
        }
    }

    private static String decode(String value) {
        try {
            return URLDecoder.decode(value.replace("+", "%2B"), StandardCharsets.UTF_8);
        } catch (IllegalArgumentException ex) {
            throw CameraSourceRules.invalid();
        }
    }

    private static String secret(String value, int max) {
        return value == null
                ? null
                : CameraSourceRules.secret(
                        new com.streamfusion.platform.camera.pojo.dto.SecretWrite("REPLACE", value),
                        null,
                        max,
                        false);
    }

    private static void credentials(String username, String password) {
        if ((username == null) != (password == null)) throw CameraSourceRules.invalid();
    }

    private static String unbracket(String value) {
        return value.startsWith("[") ? value.substring(1, value.length() - 1) : value;
    }

    private static String authority(String host, int port) {
        return (host.contains(":") ? "[" + host + "]" : host) + ":" + port;
    }
}
