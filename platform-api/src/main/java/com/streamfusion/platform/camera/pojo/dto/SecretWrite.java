package com.streamfusion.platform.camera.pojo.dto;

import io.swagger.v3.oas.annotations.media.Schema;

/** Intentionally has no generated toString: these values must never enter logs. */
public record SecretWrite(
        @Schema(description = "KEEP、REPLACE或CLEAR") String action,
        @Schema(description = "仅REPLACE提供的秘密值，不返回旧值") String value) {
    @Override
    public String toString() {
        return "SecretWrite[redacted]";
    }
}
