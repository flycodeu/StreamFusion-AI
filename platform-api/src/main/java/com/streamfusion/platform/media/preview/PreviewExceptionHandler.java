package com.streamfusion.platform.media.preview;

import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;

@RestControllerAdvice(assignableTypes = PreviewController.class)
public class PreviewExceptionHandler {
    @ExceptionHandler(PreviewFailure.class)
    public ResponseEntity<R<Void>> failure(PreviewFailure failure) {
        ErrorCode code =
                switch (failure.reason()) {
                    case "LEASE_NOT_FOUND", "LEASE_EXPIRED" -> ErrorCode.NOT_FOUND;
                    case "VALIDATION_ERROR" -> ErrorCode.VALIDATION_ERROR;
                    case "SIGNAL_ALREADY_CREATED" -> ErrorCode.CONFLICT;
                    default -> ErrorCode.DEPENDENCY_UNAVAILABLE;
                };
        return ResponseEntity.status(code.httpStatus()).body(R.error(code));
    }
}
