package com.streamfusion.platform.media.preview.source;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.media.preview.PreviewFailure;
import java.net.URI;
import org.springframework.stereotype.Component;

@Component
public class RtspStreamLocatorAdapter implements StreamLocatorAdapter {
    @Override
    public String type() {
        return "RTSP";
    }

    @Override
    public URI resolve(CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        String host =
                "SOURCE".equals(locator.getRtspHostMode())
                        ? context.endpoint().getHost()
                        : locator.getRtspHost();
        int port =
                "SOURCE".equals(locator.getRtspHostMode())
                        ? context.endpoint().getPort()
                        : locator.getRtspPort() == null ? 554 : locator.getRtspPort();
        if (port < 0) port = 554;
        String path = config.path("encodedPath").asText();
        String query = config.path("rawQuery").asText();
        if (!path.startsWith("/")
                || path.contains("?")
                || path.contains("#")
                || query.contains("#")) throw new PreviewFailure("LOCATOR_INVALID");
        return URI.create(
                "rtsp://"
                        + (host.contains(":") && !host.startsWith("[") ? "[" + host + "]" : host)
                        + ":"
                        + port
                        + path
                        + (query.isEmpty() ? "" : "?" + query));
    }
}
