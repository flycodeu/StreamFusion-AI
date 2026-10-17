package com.streamfusion.platform.camera.access.adapter;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.util.Map;

/** Read-only protocol discovery. Persistence and authorization belong to the caller. */
public interface CameraAccessAdapter {
    String type();

    CameraAdapterDescriptor descriptor();

    CameraAccessCatalog discover(CameraAccessContext context);

    /** Persistent protocol fields; stable channel/profile identity is added by the caller. */
    default Map<String, Object> locatorConfiguration(CameraAccessCatalog.Locator locator) {
        throw CameraSourceRules.invalid();
    }

    /** Device credentials are optional, but a username/password pair is indivisible. */
    default void validateCredentials(String username, String password) {
        if ((username == null) != (password == null)) throw CameraSourceRules.invalid();
    }
}
