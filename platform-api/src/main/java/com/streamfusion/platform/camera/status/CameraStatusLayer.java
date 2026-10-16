package com.streamfusion.platform.camera.status;

public enum CameraStatusLayer {
    CONTROL("RTSP_DESCRIBE"),
    MEDIA("ZLM_RTP_FRAMES"),
    UPSTREAM("HIK_PLATFORM_ONLINE");

    private final String probeKind;

    CameraStatusLayer(String probeKind) {
        this.probeKind = probeKind;
    }

    public String probeKind() {
        return probeKind;
    }
}
