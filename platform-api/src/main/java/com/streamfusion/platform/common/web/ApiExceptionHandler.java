package com.streamfusion.platform.common.web;

import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import jakarta.servlet.http.HttpServletRequest;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatusCode;
import org.springframework.http.ResponseEntity;
import org.springframework.security.access.AccessDeniedException;
import org.springframework.security.authentication.AuthenticationTrustResolverImpl;
import org.springframework.security.core.AuthenticationException;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.validation.BindException;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.context.request.ServletWebRequest;
import org.springframework.web.context.request.WebRequest;
import org.springframework.web.servlet.mvc.method.annotation.ResponseEntityExceptionHandler;

@RestControllerAdvice
public class ApiExceptionHandler extends ResponseEntityExceptionHandler {
    private static final Logger LOG = LoggerFactory.getLogger(ApiExceptionHandler.class);
    private final ApiErrorWriter writer;

    public ApiExceptionHandler(ApiErrorWriter writer) {
        this.writer = writer;
    }

    @Override
    protected ResponseEntity<Object> handleExceptionInternal(
            Exception ex,
            Object body,
            HttpHeaders headers,
            HttpStatusCode statusCode,
            WebRequest request) {
        var servlet = (ServletWebRequest) request;
        if (servlet.getResponse() != null && servlet.getResponse().isCommitted()) return null;
        if (statusCode.is5xxServerError()) LOG.error("Request failed", ex);
        Object details = null;
        if (ex instanceof BindException binding && statusCode.value() == 400) {
            List<ValidationDetails.FieldError> fields =
                    binding.getFieldErrors().stream()
                            .map(error -> error.getField())
                            .filter(field -> field.matches("[A-Za-z][A-Za-z0-9_]{0,63}"))
                            .distinct()
                            .limit(20)
                            .map(ValidationDetails.FieldError::invalid)
                            .toList();
            if (!fields.isEmpty()) details = new ValidationDetails(fields);
        }
        return writer.entity(
                servlet.getRequest(),
                statusCode.value(),
                ErrorCode.fromStatus(statusCode.value()),
                details,
                headers);
    }

    @ExceptionHandler(BusinessException.class)
    public ResponseEntity<Object> business(BusinessException ex, HttpServletRequest request) {
        LOG.warn("Business request rejected code={}", ex.code());
        return writer.entity(
                request, ex.code().httpStatus(), ex.code(), ex.safeDetails(), new HttpHeaders());
    }

    @ExceptionHandler(AuthenticationException.class)
    public ResponseEntity<Object> unauthenticated(
            AuthenticationException ex, HttpServletRequest request) {
        return writer.entity(request, 401, ErrorCode.UNAUTHORIZED, null, new HttpHeaders());
    }

    @ExceptionHandler(AccessDeniedException.class)
    public ResponseEntity<Object> denied(AccessDeniedException ex, HttpServletRequest request) {
        var authentication = SecurityContextHolder.getContext().getAuthentication();
        var trust = new AuthenticationTrustResolverImpl();
        ErrorCode code =
                authentication == null
                                || !authentication.isAuthenticated()
                                || trust.isAnonymous(authentication)
                        ? ErrorCode.UNAUTHORIZED
                        : ErrorCode.FORBIDDEN;
        return writer.entity(request, code.httpStatus(), code, null, new HttpHeaders());
    }

    @ExceptionHandler(Exception.class)
    public ResponseEntity<Object> unexpected(Exception ex, HttpServletRequest request) {
        LOG.error("Unhandled request exception", ex);
        return writer.entity(request, 500, ErrorCode.INTERNAL_ERROR, null, new HttpHeaders());
    }
}
