package com.streamfusion.platform.media.preview.source;

import com.fasterxml.jackson.databind.JsonNode;
import com.streamfusion.platform.camera.access.adapter.CameraAccessContext;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import java.net.URI;

/** Resolves a stored protocol identity on demand; the URI is internal and never persisted. */
public interface StreamLocatorAdapter {
    String type();

    URI resolve(CameraLocatorEntity locator, JsonNode config, CameraAccessContext context);
}
