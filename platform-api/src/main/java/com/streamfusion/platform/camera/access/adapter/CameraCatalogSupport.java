package com.streamfusion.platform.camera.access.adapter;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.Set;

final class CameraCatalogSupport {
    private CameraCatalogSupport() {}

    static CameraAccessCatalog.Device device(
            String name,
            String manufacturer,
            String model,
            String firmware,
            String serial,
            Set<String> warnings) {
        String key;
        if (serial == null || serial.isBlank()) {
            key = "source-local-device:identity-unknown";
            warnings.add("DEVICE_IDENTITY_UNVERIFIED");
        } else {
            key =
                    "serial:"
                            + CameraHttpAuthentication.hash(
                                    "SHA-256", serial, StandardCharsets.UTF_8);
        }
        return new CameraAccessCatalog.Device(key, name, manufacturer, model, firmware, serial);
    }

    static URI rtsp(CameraAccessContext context, String path, String query) {
        try {
            if (context.rtspPort() < 1 || context.rtspPort() > 65535)
                throw new CameraAdapterException("INVALID_RTSP_PORT");
            return new URI(
                    "rtsp",
                    null,
                    context.endpoint().getHost(),
                    context.rtspPort(),
                    path,
                    query,
                    null);
        } catch (java.net.URISyntaxException ex) {
            throw new CameraAdapterException("INVALID_STREAM_URI");
        }
    }
}
