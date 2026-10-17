package com.streamfusion.platform.camera.access.adapter;

import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;

final class CameraCatalogSupport {
    private CameraCatalogSupport() {}

    static Map<String, Object> tokenLocator(CameraAccessCatalog.Locator locator) {
        return Map.of("profileToken", opaque(locator.profileToken()));
    }

    static Map<String, Object> streamLocator(CameraAccessCatalog.Locator locator) {
        if (locator.streamType() == null || locator.streamType() < 0 || locator.streamType() > 255)
            throw com.streamfusion.platform.camera.service.CameraSourceRules.invalid();
        var config = new LinkedHashMap<String, Object>();
        config.put("streamType", locator.streamType());
        if (locator.profileToken() != null)
            config.put("resourceToken", opaque(locator.profileToken()));
        return config;
    }

    private static String opaque(String value) {
        if (value == null
                || value.isEmpty()
                || value.codePointCount(0, value.length()) > 512
                || value.codePoints().anyMatch(Character::isISOControl))
            throw com.streamfusion.platform.camera.service.CameraSourceRules.invalid();
        return value;
    }

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
                            + CameraRequestAuthentication.hash(
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
