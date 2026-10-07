package com.streamfusion.platform.camera.access.service;

import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.access.adapter.CameraAccessCatalog;
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
        if (input == null || !adapters.methods().contains(input.method()))
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
        String policy = input.networkPolicyKey();
        if (policy == null && properties.getNetworkPolicies().size() == 1)
            policy = properties.getNetworkPolicies().keySet().iterator().next();
        rules.policy(policy);
        String username = secret(input.username(), 128), password = secret(input.password(), 512);
        String name = CameraSourceRules.text(input.name(), 100, false);
        int rtspPort = input.rtspPort() == null ? 554 : input.rtspPort();
        CameraSourceRules.port(rtspPort);
        if ("RTSP".equals(input.method())) {
            if (name == null
                    || name.isBlank()
                    || input.rtspUrls() == null
                    || input.rtspUrls().isEmpty()
                    || input.rtspUrls().size() > 8) throw CameraSourceRules.invalid();
            var urls = new ArrayList<String>();
            String host = null;
            int port = 554;
            for (String text : input.rtspUrls()) {
                URI uri = parseRtsp(text);
                String nextHost = rules.host(unbracket(uri.getHost()), policy);
                int nextPort = uri.getPort() == -1 ? 554 : uri.getPort();
                CameraSourceRules.port(nextPort);
                if (host != null && (!host.equals(nextHost) || port != nextPort))
                    throw CameraSourceRules.invalid();
                host = nextHost;
                port = nextPort;
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
                        "rtsp://"
                                + authority(host, port)
                                + uri.getRawPath()
                                + (uri.getRawQuery() == null ? "" : "?" + uri.getRawQuery());
                if (urls.contains(clean)) throw CameraSourceRules.invalid();
                urls.add(clean);
            }
            credentials(username, password);
            if (input.sourceId() != null
                    && (!Objects.equals(host, input.host()) || !Objects.equals(port, input.port())))
                throw CameraSourceRules.invalid();
            return new CameraConnection(
                    "RTSP",
                    name,
                    host,
                    port,
                    "rtsp",
                    username,
                    password,
                    rtspPort,
                    policy,
                    input.sourceId(),
                    input.sourceVersion(),
                    List.copyOf(urls),
                    page,
                    size);
        }
        if (input.rtspUrls() != null && !input.rtspUrls().isEmpty())
            throw CameraSourceRules.invalid();
        String scheme = input.scheme() == null ? (platform ? "https" : "http") : input.scheme();
        if (!List.of("http", "https").contains(scheme)) throw CameraSourceRules.invalid();
        String host = rules.host(input.host(), policy);
        int port = input.port() == null ? ("https".equals(scheme) ? 443 : 80) : input.port();
        CameraSourceRules.port(port);
        credentials(username, password);
        if (platform && username == null) throw CameraSourceRules.invalid();
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
                size);
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

    public CameraAccessCatalog rtspCatalog(CameraConnection connection) {
        var profiles = new ArrayList<CameraAccessCatalog.Profile>();
        for (int i = 0; i < connection.rtspUrls().size(); i++) {
            profiles.add(
                    new CameraAccessCatalog.Profile(
                            "rtsp-profile:" + stableKey(connection.rtspUrls().get(i)),
                            "码流 " + (i + 1),
                            "UNKNOWN",
                            null,
                            null,
                            null,
                            null,
                            null,
                            new CameraAccessCatalog.Locator(
                                    "RTSP", URI.create(connection.rtspUrls().get(i)), null, null)));
        }
        return new CameraAccessCatalog(
                "RTSP",
                null,
                List.of(
                        new CameraAccessCatalog.Channel(
                                "rtsp-config:"
                                        + stableKey(
                                                String.join(
                                                        "\n",
                                                        connection.rtspUrls().stream()
                                                                .sorted()
                                                                .toList())),
                                connection.name(),
                                profiles,
                                false)),
                true,
                List.of("RTSP_CONFIGURATION_ONLY"));
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

    public static URI parseRtsp(String value) {
        if (value == null
                || value.length() > 8192
                || value.codePoints().anyMatch(Character::isISOControl))
            throw CameraSourceRules.invalid();
        try {
            URI uri = URI.create(value.strip());
            if (!"rtsp".equalsIgnoreCase(uri.getScheme())
                    || uri.getHost() == null
                    || uri.getRawFragment() != null
                    || uri.getRawPath() == null
                    || !uri.getRawPath().startsWith("/")) throw CameraSourceRules.invalid();
            return uri;
        } catch (IllegalArgumentException ex) {
            throw CameraSourceRules.invalid();
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
