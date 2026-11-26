package com.streamfusion.platform.camera.locator;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.access.adapter.HikPlatformApi;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import java.net.URI;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

@Component
@RequiredArgsConstructor
public class HikPlatformStreamLocatorAdapter implements StreamLocatorAdapter {
    private final HikPlatformApi api;

    @Override
    public String type() {
        return "HIK_PLATFORM";
    }

    @Override
    public URI resolve(CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        String code = config.path("channelKey").asText();
        HikPlatformApi.requireCode(code);
        int streamType = config.path("streamType").asInt(-1);
        if (streamType != 0 && streamType != 1) throw new CameraAdapterException("LOCATOR_INVALID");
        var data =
                api.post(
                        context,
                        "/artemis/api/video/v2/cameras/previewURLs",
                        Map.of(
                                "cameraIndexCode",
                                code,
                                "streamType",
                                streamType,
                                "protocol",
                                "rtsp",
                                "transmode",
                                1,
                                "streamform",
                                "rtp"));
        try {
            var uri = URI.create(data.path("url").asText());
            if (!"rtsp".equalsIgnoreCase(uri.getScheme())
                    || uri.getHost() == null
                    || uri.getFragment() != null)
                throw new CameraAdapterException("LOCATOR_INVALID");
            return uri;
        } catch (IllegalArgumentException ex) {
            throw new CameraAdapterException("LOCATOR_INVALID");
        }
    }
}
