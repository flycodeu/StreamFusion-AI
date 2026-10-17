package com.streamfusion.platform.media.preview.node;

import java.net.URI;
import java.nio.file.Path;

/** An administrator-owned HTTP API on a dedicated private-network ZLM instance. */
public record ZlmDirectConnection(
        String nodeId, String name, URI apiUri, Path secretFile, String mediaAddress) {
    public ZlmDirectConnection(String nodeId, URI apiUri, Path secretFile, String mediaAddress) {
        this(nodeId, nodeId, apiUri, secretFile, mediaAddress);
    }

    public ZlmDirectConnection {
        if (nodeId == null
                || !nodeId.matches("zlm-[a-f0-9]{16}")
                || name == null
                || name.isBlank()
                || name.length() > 80
                || apiUri == null
                || !"http".equals(apiUri.getScheme())
                || apiUri.getHost() == null
                || apiUri.getPort() < 1
                || apiUri.getPort() > 65535
                || apiUri.getRawUserInfo() != null
                || !apiUri.getRawPath().isEmpty()
                || apiUri.getRawQuery() != null
                || apiUri.getRawFragment() != null
                || secretFile == null
                || !secretFile.isAbsolute()
                || mediaAddress == null
                || !mediaAddress.matches("[0-9.]{7,15}"))
            throw new IllegalArgumentException("Invalid dedicated ZLM connection");
        privateIpv4(apiUri.getHost());
        privateIpv4(mediaAddress);
    }

    public static void privateIpv4(String value) {
        if (value == null || !value.matches("(0|[1-9][0-9]{0,2})(\\.(0|[1-9][0-9]{0,2})){3}"))
            throw new IllegalArgumentException("A private IPv4 address is required");
        String[] parts = value.split("\\.");
        int a = Integer.parseInt(parts[0]);
        int b = Integer.parseInt(parts[1]);
        for (String part : parts)
            if (Integer.parseInt(part) > 255) throw new IllegalArgumentException("Invalid IPv4");
        if (!(a == 10 || a == 127 || a == 172 && b >= 16 && b <= 31 || a == 192 && b == 168))
            throw new IllegalArgumentException("A private IPv4 address is required");
    }
}
