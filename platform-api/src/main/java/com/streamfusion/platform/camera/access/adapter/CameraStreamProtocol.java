package com.streamfusion.platform.camera.access.adapter;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.net.URI;
import java.util.Arrays;
import java.util.List;
import java.util.Locale;

/** One URL policy for discovery, storage and preview. RTSP keeps its existing contract. */
public enum CameraStreamProtocol {
    RTSP("已知 RTSP 地址", List.of("rtsp"), 554),
    RTMP("RTMP", List.of("rtmp"), 1935),
    FLV("HTTP-FLV", List.of("http", "https"), 80),
    HLS("HLS", List.of("http", "https"), 80),
    CRTSP("cRTSP（相机主动推流）", List.of("rtsp"), 554),
    GB28181("GB28181", List.of("gb28181"), 554);

    private final String label;
    private final List<String> schemes;
    private final int defaultPort;

    CameraStreamProtocol(String label, List<String> schemes, int defaultPort) {
        this.label = label;
        this.schemes = schemes;
        this.defaultPort = defaultPort;
    }

    public String label() {
        return label;
    }

    public static boolean supports(String kind) {
        return kind != null && Arrays.stream(values()).anyMatch(p -> p.name().equals(kind));
    }

    public static boolean stream(String kind) {
        return supports(kind) && !"RTSP".equals(kind);
    }

    public boolean accepts(String scheme) {
        return scheme != null && schemes.contains(scheme.toLowerCase(Locale.ROOT));
    }

    public int port(URI uri) {
        return uri.getPort() >= 0
                ? uri.getPort()
                : switch (uri.getScheme().toLowerCase(Locale.ROOT)) {
                    case "https", "rtmps" -> 443;
                    default -> defaultPort;
                };
    }

    public URI parse(String value) {
        try {
            if (value == null
                    || value.length() > 8192
                    || value.codePoints().anyMatch(Character::isISOControl))
                throw CameraSourceRules.invalid();
            URI uri = URI.create(value.strip());
            if (!accepts(uri.getScheme())
                    || uri.getHost() == null
                    || uri.getRawFragment() != null
                    || uri.getRawPath() == null
                    || !uri.getRawPath().startsWith("/")) throw CameraSourceRules.invalid();
            CameraSourceRules.port(port(uri));
            if (this == GB28181
                    && (!uri.getRawPath().matches("/[0-9]{20}/[0-9]{20}")
                            || uri.getRawQuery() != null
                            || uri.getRawUserInfo() != null)) throw CameraSourceRules.invalid();
            if (this == CRTSP
                    && (!uri.getRawPath().matches("/sf_ingress/[a-f0-9]{32}")
                            || uri.getRawQuery() != null
                            || uri.getRawUserInfo() != null)) throw CameraSourceRules.invalid();
            if ((this == HLS || this == FLV)
                    && !uri.getPath()
                            .toLowerCase(Locale.ROOT)
                            .endsWith(this == HLS ? ".m3u8" : ".flv"))
                throw CameraSourceRules.invalid();
            return uri;
        } catch (IllegalArgumentException ex) {
            throw CameraSourceRules.invalid();
        }
    }
}
