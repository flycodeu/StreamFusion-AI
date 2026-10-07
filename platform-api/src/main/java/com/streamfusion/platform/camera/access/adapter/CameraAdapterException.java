package com.streamfusion.platform.camera.access.adapter;

/**
 * Fixed diagnostics only: upstream bodies, URLs, credentials and exception messages are excluded.
 */
public final class CameraAdapterException extends RuntimeException {
    private final String reasonCode;

    public CameraAdapterException(String reasonCode) {
        super(reasonCode);
        this.reasonCode = reasonCode;
    }

    public String reasonCode() {
        return reasonCode;
    }
}
