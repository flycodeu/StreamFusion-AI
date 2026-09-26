package com.streamfusion.platform.auth.security;

import com.streamfusion.platform.auth.controller.AuthController;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.ServletOutputStream;
import jakarta.servlet.WriteListener;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.io.OutputStreamWriter;
import java.io.PrintWriter;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.stereotype.Component;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;
import org.springframework.web.filter.OncePerRequestFilter;
import org.springframework.web.util.ContentCachingResponseWrapper;

/** Commits login SQL after Spring Session's final save, before releasing any response bytes. */
@Component
@Order(Ordered.HIGHEST_PRECEDENCE + 35)
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
public class LoginCommitFilter extends OncePerRequestFilter {
    private static final Logger LOG = LoggerFactory.getLogger(LoginCommitFilter.class);
    private final TransactionTemplate transactions;
    private final ApiErrorWriter errors;

    public LoginCommitFilter(PlatformTransactionManager manager, ApiErrorWriter errors) {
        transactions = new TransactionTemplate(manager);
        this.errors = errors;
    }

    @Override
    protected boolean shouldNotFilter(HttpServletRequest request) {
        String path = request.getRequestURI().substring(request.getContextPath().length());
        return !"POST".equals(request.getMethod()) || !"/auth/login/secure".equals(path);
    }

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        var buffered = new LoginResponse(response);
        try {
            transactions.executeWithoutResult(
                    status -> {
                        try {
                            chain.doFilter(request, buffered);
                            buffered.finishWriting();
                        } catch (IOException | ServletException error) {
                            throw new FilterFailure(error);
                        }
                        boolean establishing =
                                Boolean.TRUE.equals(
                                        request.getAttribute(AuthController.LOGIN_ESTABLISHING));
                        if (status.isRollbackOnly() && buffered.getStatus() < 400) {
                            throw new IllegalStateException("Login transaction cannot commit");
                        }
                        // Wrong credentials commit their failure counters and audit. Failures after
                        // establishing a new session must preserve the previous login instead.
                        if (status.isRollbackOnly()
                                || (establishing && buffered.getStatus() >= 400)) {
                            status.setRollbackOnly();
                            response.setHeader("Set-Cookie", null);
                        }
                    });
        } catch (RuntimeException failure) {
            SessionCookieProtectionFilter.revoked(request);
            // ContentCachingResponseWrapper buffers body bytes, while headers are only staged
            // in the uncommitted servlet response. Reset clears both, including new cookies.
            resetFailedResponse(buffered, response);
            response.setHeader("X-Trace-Id", (String) request.getAttribute("traceId"));
            if (failure instanceof FilterFailure wrapped) {
                if (wrapped.getCause() instanceof IOException io) throw io;
                if (wrapped.getCause() instanceof ServletException servlet) throw servlet;
            }
            ErrorCode code =
                    SessionDependencyFilter.dependencyFailure(failure)
                            ? ErrorCode.DEPENDENCY_UNAVAILABLE
                            : ErrorCode.INTERNAL_ERROR;
            LOG.warn("Login commit did not complete code={}", code);
            errors.write(request, response, code);
            return;
        }
        buffered.copyBodyToResponse();
    }

    private static void resetFailedResponse(LoginResponse buffered, HttpServletResponse response) {
        var preserved = new LinkedHashMap<String, List<String>>();
        for (String name :
                List.of(
                        "Cache-Control",
                        "Pragma",
                        "Expires",
                        "X-Content-Type-Options",
                        "X-Frame-Options",
                        "Strict-Transport-Security",
                        "Content-Security-Policy",
                        "Referrer-Policy",
                        "Permissions-Policy",
                        "X-XSS-Protection")) {
            if (response.containsHeader(name))
                preserved.put(name, List.copyOf(response.getHeaders(name)));
        }
        buffered.reset();
        preserved.forEach(
                (name, values) -> values.forEach(value -> response.addHeader(name, value)));
    }

    private static final class FilterFailure extends RuntimeException {
        private FilterFailure(Exception cause) {
            super(cause);
        }
    }

    /** A login response is small; never buffer an unbounded application response. */
    private static final class LoginResponse extends ContentCachingResponseWrapper {
        private static final int MAX_BYTES = 64 * 1024;
        private ServletOutputStream boundedStream;
        private PrintWriter writer;

        private LoginResponse(HttpServletResponse response) {
            super(response);
        }

        @Override
        public ServletOutputStream getOutputStream() throws IOException {
            if (boundedStream == null) {
                var target = super.getOutputStream();
                boundedStream =
                        new ServletOutputStream() {
                            private void reserve(int length) throws IOException {
                                if (length > MAX_BYTES - getContentSize())
                                    throw new IOException("Login response exceeds size limit");
                            }

                            @Override
                            public boolean isReady() {
                                return target.isReady();
                            }

                            @Override
                            public void setWriteListener(WriteListener listener) {
                                target.setWriteListener(listener);
                            }

                            @Override
                            public void write(int value) throws IOException {
                                reserve(1);
                                target.write(value);
                            }

                            @Override
                            public void write(byte[] value, int offset, int length)
                                    throws IOException {
                                reserve(length);
                                target.write(value, offset, length);
                            }
                        };
            }
            return boundedStream;
        }

        @Override
        public PrintWriter getWriter() throws IOException {
            if (writer == null)
                writer =
                        new PrintWriter(
                                new OutputStreamWriter(getOutputStream(), StandardCharsets.UTF_8));
            return writer;
        }

        private void finishWriting() throws IOException {
            if (writer != null) {
                writer.flush();
                if (writer.checkError())
                    throw new IOException("Login response could not be buffered");
            }
        }

        @Override
        public void setContentLength(int length) {
            if (length > MAX_BYTES)
                throw new IllegalArgumentException("Login response exceeds size limit");
            super.setContentLength(length);
        }

        @Override
        public void setContentLengthLong(long length) {
            if (length > MAX_BYTES)
                throw new IllegalArgumentException("Login response exceeds size limit");
            super.setContentLengthLong(length);
        }

        @Override
        public void sendError(int status) {
            resetBuffer();
            setStatus(status);
        }

        @Override
        public void sendError(int status, String message) {
            sendError(status);
        }

        @Override
        public void sendRedirect(String location) {
            resetBuffer();
            setStatus(HttpServletResponse.SC_FOUND);
            setHeader("Location", location);
        }
    }
}
