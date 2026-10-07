package com.streamfusion.platform.camera.access.adapter;

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
}
