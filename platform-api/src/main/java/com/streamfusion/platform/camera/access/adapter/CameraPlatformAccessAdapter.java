package com.streamfusion.platform.camera.access.adapter;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import java.util.List;

/** Capabilities consumed by the existing platform connection and region browser. */
public interface CameraPlatformAccessAdapter extends CameraAccessAdapter {
    record Region(String indexCode, String parentIndexCode, String name) {}

    record Regions(List<Region> items, long total, int page, int size) {
        public Regions {
            items = List.copyOf(items);
        }
    }

    void validateRegionCode(String code);

    Regions regions(CameraAccessContext context, String parentCode, int page, int size);

    @Override
    default void validateCredentials(String username, String password) {
        CameraAccessAdapter.super.validateCredentials(username, password);
        if (username == null) throw CameraSourceRules.invalid();
    }
}
