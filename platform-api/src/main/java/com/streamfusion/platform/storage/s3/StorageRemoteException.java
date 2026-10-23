package com.streamfusion.platform.storage.s3;

/** Deliberately contains only a bounded stage/reason, never URI, response body or signature. */
public class StorageRemoteException extends RuntimeException {
    private final String reason;
    private final int status;

    public StorageRemoteException(String reason, int status) {
        super(reason);
        this.reason = reason;
        this.status = status;
    }

    public String reason() {
        return reason;
    }

    public int status() {
        return status;
    }
}
