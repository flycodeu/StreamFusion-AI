package com.streamfusion.platform.media.preview.source;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraStreamProtocol;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import java.net.URI;
import java.util.List;
import org.springframework.stereotype.Component;

/** Resolves immutable URL configuration; no network calls on read-only status queries. */
@Component
public final class UrlStreamLocatorAdapter implements StreamLocatorAdapter {
    public String type() {
        return "RTMP";
    }

    public List<String> types() {
        return List.of("RTMP", "FLV", "HLS", "CRTSP", "GB28181");
    }

    public URI resolve(CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        return CameraStreamProtocol.valueOf(locator.getLocatorKind())
                .parse(config.path("uri").asText());
    }
}
