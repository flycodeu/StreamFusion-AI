package com.streamfusion.platform.camera.access.adapter;

/** The registered implementation owns its UI category and supported control operations. */
public record CameraAdapterDescriptor(
        String type,
        String label,
        String category,
        String inputKind,
        boolean autoDetect,
        int autoOrder,
        boolean paged,
        String endpointPath,
        String endpointPurpose) {
    public CameraAdapterDescriptor(
            String type,
            String label,
            String category,
            String inputKind,
            boolean autoDetect,
            int autoOrder,
            boolean paged) {
        this(
                type,
                label,
                category,
                inputKind,
                autoDetect,
                autoOrder,
                paged,
                "PLATFORM".equals(category) ? "/" : "",
                "PLATFORM".equals(category)
                        ? "PLATFORM_HTTP"
                        : "RTSP".equals(category) ? "RTSP" : "VENDOR_HTTP");
    }
}
