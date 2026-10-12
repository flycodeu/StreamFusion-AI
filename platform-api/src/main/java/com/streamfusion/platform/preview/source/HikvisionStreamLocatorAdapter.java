package com.streamfusion.platform.preview.source;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.preview.PreviewFailure;
import java.net.URI;
import org.springframework.stereotype.Component;

@Component
public class HikvisionStreamLocatorAdapter implements StreamLocatorAdapter {
    @Override
    public String type() {
        return "HIKVISION";
    }

    @Override
    public URI resolve(CameraLocatorEntity locator, JsonNode config, CameraAccessContext context) {
        String token = config.path("profileToken").asText();
        if (!token.matches("[1-9][0-9]{0,8}")) throw new PreviewFailure("LOCATOR_INVALID");
        return URI.create(
                "rtsp://"
                        + context.endpoint().getHost()
                        + ":"
                        + context.rtspPort()
                        + "/Streaming/Channels/"
                        + token);
    }
}
