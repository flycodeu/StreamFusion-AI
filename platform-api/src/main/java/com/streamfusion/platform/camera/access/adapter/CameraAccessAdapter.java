package com.streamfusion.platform.camera.access.adapter;

/** Read-only protocol discovery. Persistence and authorization belong to the caller. */
public interface CameraAccessAdapter {
    String type();

    CameraAdapterDescriptor descriptor();

    CameraAccessCatalog discover(CameraAccessContext context);
}
