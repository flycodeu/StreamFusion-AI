package com.streamfusion.platform.storage;

import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.storage.s3.StorageRemoteException;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

@Order(Ordered.HIGHEST_PRECEDENCE)
@RestControllerAdvice(basePackages = "com.streamfusion.platform.storage")
public class StorageExceptionHandler {
    @ExceptionHandler(StorageRemoteException.class)
    public ResponseEntity<R<Void>> remote(StorageRemoteException failure) {
        ErrorCode code =
                failure.reason().equals("STORAGE_FILE_EXISTS")
                        ? ErrorCode.STORAGE_FILE_EXISTS
                        : failure.status() == 404
                                ? ErrorCode.NOT_FOUND
                                : failure.status() == 412
                                        ? ErrorCode.STORAGE_OBJECT_CHANGED
                                        : failure.status() == 416
                                                ? ErrorCode.STORAGE_RANGE_INVALID
                                                : failure.status() == 429
                                                        ? ErrorCode.RATE_LIMITED
                                                        : ErrorCode.STORAGE_UPSTREAM_ERROR;
        return ResponseEntity.status(code.httpStatus()).body(R.error(code));
    }
}
