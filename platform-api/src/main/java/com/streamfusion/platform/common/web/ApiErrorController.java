package com.streamfusion.platform.common.web;

import com.streamfusion.platform.common.exception.ErrorCode;
import io.swagger.v3.oas.annotations.Hidden;
import jakarta.servlet.RequestDispatcher;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.springframework.boot.web.servlet.error.ErrorController;
import org.springframework.http.HttpHeaders;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@Hidden
public class ApiErrorController implements ErrorController {
    private final ApiErrorWriter writer;

    public ApiErrorController(ApiErrorWriter writer) {
        this.writer = writer;
    }

    @RequestMapping("/error")
    public ResponseEntity<Object> error(HttpServletRequest request, HttpServletResponse response) {
        if (response.isCommitted()) return null;
        Object value = request.getAttribute(RequestDispatcher.ERROR_STATUS_CODE);
        int status =
                value instanceof Integer number && number >= 400 && number <= 599 ? number : 404;
        return writer.entity(
                request, status, ErrorCode.fromStatus(status), null, new HttpHeaders());
    }
}
