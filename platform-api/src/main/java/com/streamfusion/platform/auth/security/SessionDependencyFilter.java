package com.streamfusion.platform.auth.security;

import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import lombok.RequiredArgsConstructor;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.dao.DataAccessResourceFailureException;
import org.springframework.dao.QueryTimeoutException;
import org.springframework.stereotype.Component;
import org.springframework.transaction.CannotCreateTransactionException;
import org.springframework.web.filter.OncePerRequestFilter;

/** Surround Spring Session too: repository failures may happen before MVC/security handling. */
@Component
@Order(Ordered.HIGHEST_PRECEDENCE + 10)
@RequiredArgsConstructor
public class SessionDependencyFilter extends OncePerRequestFilter {
    private static final Logger LOG = LoggerFactory.getLogger(SessionDependencyFilter.class);
    private final ApiErrorWriter writer;

    @Override
    protected void doFilterInternal(
            HttpServletRequest request, HttpServletResponse response, FilterChain chain)
            throws ServletException, IOException {
        try {
            chain.doFilter(request, response);
        } catch (RuntimeException ex) {
            if (!dependencyFailure(ex)) throw ex;
            LOG.warn("Identity dependency unavailable");
            if (!response.isCommitted()) {
                response.resetBuffer();
                response.setHeader("Content-Length", null);
            }
            writer.write(request, response, ErrorCode.DEPENDENCY_UNAVAILABLE);
        }
    }

    public static boolean dependencyFailure(Throwable error) {
        for (Throwable item = error; item != null; item = item.getCause()) {
            if (item instanceof DataAccessResourceFailureException
                    || item instanceof QueryTimeoutException
                    || item instanceof CannotCreateTransactionException) return true;
            if (item == item.getCause()) break;
        }
        return false;
    }
}
