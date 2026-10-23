package com.streamfusion.platform.monitor.diagnostics;

import static org.mockito.Mockito.*;

import com.streamfusion.platform.common.security.ModuleRegistry;
import com.streamfusion.platform.common.web.TraceFilter;
import com.streamfusion.platform.monitor.security.SecurityMonitor;
import jakarta.servlet.FilterChain;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;

class TraceDiagnosticTest {
    @Test
    void syncRejectionIsRecordedOnceWithFinalStatus() throws Exception {
        var logs = mock(DiagnosticLog.class);
        var security = mock(SecurityMonitor.class);
        var filter = new TraceFilter(mock(ModuleRegistry.class), logs, security);
        var request = new MockHttpServletRequest("POST", "/camera-status/plans");
        var response = new MockHttpServletResponse();
        FilterChain chain =
                (req, res) -> ((jakarta.servlet.http.HttpServletResponse) res).setStatus(409);
        filter.doFilter(request, response, chain);
        filter.doFilter(request, response, chain);
        verify(logs, times(1)).request(eq(request), eq(409), anyLong());
        verify(security, times(1)).observe(request, 409, false);
    }

    @Test
    void asyncCompletionDoesNotLogInitialSuccessOrDuplicateDispatch() throws Exception {
        var logs = mock(DiagnosticLog.class);
        var security = mock(SecurityMonitor.class);
        var filter = new TraceFilter(mock(ModuleRegistry.class), logs, security);
        var request = new MockHttpServletRequest("GET", "/async");
        request.setAsyncSupported(true);
        var response = new MockHttpServletResponse();
        filter.doFilter(request, response, (req, res) -> req.startAsync(req, res));
        verifyNoInteractions(logs);
        verifyNoInteractions(security);
        response.setStatus(503);
        request.getAsyncContext().complete();
        verify(logs, times(1)).request(eq(request), eq(503), anyLong());
        verify(security, times(1)).observe(request, 503, false);
    }
}
