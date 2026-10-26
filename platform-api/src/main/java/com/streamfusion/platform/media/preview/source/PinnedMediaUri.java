package com.streamfusion.platform.media.preview.source;

import com.streamfusion.platform.media.preview.PreviewFailure;
import java.net.URI;

/**
 * Private preview-node transport metadata. The origin fragment is removed before HTTP I/O. Sockets
 * use only the numeric authority; TLS and Host use the original, validated hostname.
 */
public final class PinnedMediaUri {
    private PinnedMediaUri() {}

    public static String fragment(URI source) {
        String host = source.getHost().replace("[", "").replace("]", "");
        return ("http".equals(source.getScheme()) || "https".equals(source.getScheme()))
                        && !host.contains(":")
                ? "#sf-origin=" + host
                : "";
    }

    public static String originHost(URI source) {
        String fragment = source.getRawFragment();
        if (fragment == null) return source.getHost().replace("[", "").replace("]", "");
        if (!fragment.matches("sf-origin=[A-Za-z0-9][A-Za-z0-9.-]{0,252}"))
            throw new PreviewFailure("DESTINATION_REJECTED");
        return fragment.substring("sf-origin=".length());
    }
}
