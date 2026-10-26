package com.streamfusion.previewnode;

import com.streamfusion.platform.media.preview.PreviewFailure;
import java.net.URI;
import java.util.Set;

/** Platform already pins DNS. The node accepts only explicitly allowed numeric RTSP endpoints. */
public final class PreviewNodeSourcePolicy {
    private final Set<String> targets;

    public PreviewNodeSourcePolicy(java.util.List<String> targets) {
        this.targets =
                targets.stream()
                        .map(PreviewNodeSourcePolicy::target)
                        .collect(java.util.stream.Collectors.toUnmodifiableSet());
    }

    static String target(String endpoint) {
        try {
            var uri = URI.create("rtsp://" + endpoint);
            String host = uri.getHost();
            if (host == null
                    || uri.getUserInfo() != null
                    || !uri.getRawPath().isEmpty()
                    || uri.getQuery() != null
                    || uri.getFragment() != null
                    || uri.getPort() < 1
                    || uri.getPort() > 65535
                    || !host.matches("[0-9a-fA-F:.\\[\\]]+")) throw new IllegalArgumentException();
            // Numeric-only input cannot cause a DNS lookup; reject abbreviated/octal IPv4.
            host = host.replace("[", "").replace("]", "");
            if (!host.contains(":")) {
                if (!host.matches("(0|[1-9][0-9]{0,2})(\\.(0|[1-9][0-9]{0,2})){3}"))
                    throw new IllegalArgumentException();
                for (String part : host.split("\\."))
                    if (Integer.parseInt(part) > 255) throw new IllegalArgumentException();
            }
            return java.net.InetAddress.getByName(host).getHostAddress() + ":" + uri.getPort();
        } catch (Exception ex) {
            throw new IllegalArgumentException("Invalid numeric media endpoint");
        }
    }

    public URI require(String value) {
        try {
            if (value == null || value.length() > 8192) throw new IllegalArgumentException();
            URI uri = URI.create(value);
            if (!Set.of("rtsp", "rtmp", "http", "https").contains(uri.getScheme())
                    || uri.getFragment() != null
                            && !(Set.of("http", "https").contains(uri.getScheme())
                                    && uri.getRawFragment()
                                            .matches("sf-origin=[A-Za-z0-9][A-Za-z0-9.-]{0,252}"))
                    || uri.getHost() == null) throw new IllegalArgumentException();
            int port =
                    uri.getPort() < 0
                            ? switch (uri.getScheme()) {
                                case "http" -> 80;
                                case "https" -> 443;
                                case "rtmp" -> 1935;
                                default -> 554;
                            }
                            : uri.getPort();
            if (Set.of("http", "https").contains(uri.getScheme())
                    && (uri.getPath() == null
                            || !uri.getPath()
                                    .toLowerCase(java.util.Locale.ROOT)
                                    .matches(".*\\.(?:flv|m3u8)$")))
                throw new IllegalArgumentException();
            if (!targets.contains(target(uri.getHost() + ":" + port)))
                throw new IllegalArgumentException();
            return uri;
        } catch (IllegalArgumentException ex) {
            throw new PreviewFailure("DESTINATION_REJECTED");
        }
    }
}
