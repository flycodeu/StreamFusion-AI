package com.streamfusion.platform.media.preview.source;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.media.preview.PreviewFailure;
import java.net.URI;
import org.springframework.stereotype.Component;

@Component
public class DahuaStreamLocatorAdapter implements StreamLocatorAdapter {
    @Override
    public String type() {
        return "DAHUA";
    }

    @Override
    public URI resolve(CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        String channel = config.path("channelKey").asText();
        int stream = config.path("streamType").asInt(-1);
        var match = java.util.regex.Pattern.compile("Encode\\[([0-9]{1,8})\\]").matcher(channel);
        if (!match.matches() || stream < 0 || stream > 255)
            throw new PreviewFailure("LOCATOR_INVALID");
        int channelNumber = Integer.parseInt(match.group(1)) + 1;
        return URI.create(
                "rtsp://"
                        + context.endpoint().getHost()
                        + ":"
                        + context.rtspPort()
                        + "/cam/realmonitor?channel="
                        + channelNumber
                        + "&subtype="
                        + stream);
    }
}
