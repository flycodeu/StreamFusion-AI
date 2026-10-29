package com.streamfusion.platform.monitor.diagnostics;

import static com.streamfusion.platform.common.exception.BusinessException.error;

import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.security.ModuleRegistry;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.monitor.audit.service.AuditService;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.user.service.UserService;
import io.swagger.v3.oas.annotations.Operation;
import jakarta.servlet.http.HttpServletRequest;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.stereotype.Service;
import org.springframework.web.method.HandlerMethod;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

@Service
public class DiagnosticCaptureService {
    private final CurrentUserService current;
    private final AccessService access;
    private final UserService users;
    private final AuditService audit;
    private final Clock clock;
    private final ObjectProvider<RequestMappingHandlerMapping> mappings;
    private volatile Window window;
    private long nextPermissionCheck;

    public DiagnosticCaptureService(
            CurrentUserService current,
            AccessService access,
            UserService users,
            AuditService audit,
            Clock clock,
            @Qualifier("requestMappingHandlerMapping")
                    ObjectProvider<RequestMappingHandlerMapping> mappings) {
        this.current = current;
        this.access = access;
        this.users = users;
        this.audit = audit;
        this.clock = clock;
        this.mappings = mappings;
    }

    public record Route(String key, String label) {}

    @io.swagger.v3.oas.annotations.media.Schema(name = "DiagnosticCaptureCommand")
    public record Command(
            List<String> routes, String actorId, Integer minutes, Boolean errorsOnly) {}

    @io.swagger.v3.oas.annotations.media.Schema(name = "DiagnosticCaptureView")
    public record View(
            boolean enabled,
            String ownerId,
            Instant expiresAt,
            int matched,
            int recorded,
            int limit,
            List<String> routes,
            String actorId,
            boolean errorsOnly) {}

    static final class Window {
        final long owner;
        final Instant expires;
        final Set<String> routes;
        final String actor;
        final boolean errorsOnly;
        final AtomicInteger matched = new AtomicInteger();
        final AtomicInteger recorded = new AtomicInteger();

        Window(long owner, Instant expires, Set<String> routes, String actor, boolean errorsOnly) {
            this.owner = owner;
            this.expires = expires;
            this.routes = routes;
            this.actor = actor;
            this.errorsOnly = errorsOnly;
        }
    }

    public List<Route> routes() {
        administrator();
        return availableRoutes();
    }

    private List<Route> availableRoutes() {
        var result = new TreeMap<String, Route>();
        mappings.getObject()
                .getHandlerMethods()
                .forEach(
                        (mapping, handler) -> {
                            if (ModuleRegistry.moduleOf(handler) == null) return;
                            var operation = handler.getMethodAnnotation(Operation.class);
                            for (var path : mapping.getPatternValues())
                                if (eligible(path))
                                    for (var method : mapping.getMethodsCondition().getMethods()) {
                                        String key = method.name() + " " + path;
                                        result.put(
                                                key,
                                                new Route(
                                                        key,
                                                        operation == null
                                                                ? key
                                                                : operation.summary()));
                                    }
                        });
        return List.copyOf(result.values());
    }

    private static boolean eligible(String path) {
        return !path.startsWith("/auth")
                && !path.startsWith("/inspection-nodes")
                && !path.startsWith("/diagnostics")
                && !path.startsWith("/audit/exports")
                && !path.toLowerCase(Locale.ROOT)
                        .matches(
                                ".*(password|credential|secret|token|download|upload|stream|preview|captcha).*");
    }

    public synchronized View start(Command c, AuditContextDto context) {
        long owner = administrator();
        if (view(window).enabled())
            throw error(ErrorCode.CONFLICT).diagnosed("DIAGNOSTIC_CAPTURE_RUNNING");
        if (c == null
                || c.routes() == null
                || c.routes().isEmpty()
                || c.routes().size() > 32
                || c.minutes() == null
                || c.minutes() < 1
                || c.minutes() > 60
                || c.errorsOnly() == null) throw error(ErrorCode.VALIDATION_ERROR);
        var available =
                availableRoutes().stream()
                        .map(Route::key)
                        .collect(java.util.stream.Collectors.toSet());
        if (c.routes().stream().anyMatch(key -> key == null || !available.contains(key)))
            throw error(ErrorCode.VALIDATION_ERROR);
        String actor = c.actorId() == null || c.actorId().isBlank() ? null : c.actorId();
        if (actor != null)
            com.streamfusion.platform.common.validation.DecimalInput.id(actor, "actorId");
        var next =
                new Window(
                        owner,
                        clock.instant().plusSeconds(c.minutes() * 60L),
                        Set.copyOf(c.routes()),
                        actor,
                        c.errorsOnly());
        audit.record(
                owner,
                "DIAGNOSTIC_CAPTURE",
                null,
                "DIAGNOSTIC_CAPTURE_START",
                "SUCCESS",
                null,
                context);
        window = next;
        nextPermissionCheck = 0;
        return view(next);
    }

    public synchronized View stop(AuditContextDto context) {
        long actor = administrator();
        audit.record(
                actor,
                "DIAGNOSTIC_CAPTURE",
                null,
                "DIAGNOSTIC_CAPTURE_STOP",
                "SUCCESS",
                null,
                context);
        window = null;
        return view(null);
    }

    public View get() {
        administrator();
        return view(window);
    }

    private long administrator() {
        long id = current.requireNormal().getId();
        if (!access.hasModuleAccess(id, "diagnostics") || !access.isSuperAdmin(id))
            throw error(ErrorCode.FORBIDDEN);
        return id;
    }

    Window match(HttpServletRequest request, HandlerMethod handler, String route) {
        var w = window;
        if (w == null
                || !clock.instant().isBefore(w.expires)
                || w.matched.get() >= 1000
                || !w.routes.contains(request.getMethod() + " " + route)) return null;
        var authentication = SecurityContextHolder.getContext().getAuthentication();
        if (authentication == null
                || !(authentication.getPrincipal() instanceof SessionPrincipalDto actor))
            return null;
        if (w.actor != null && !w.actor.equals(Long.toString(actor.getUserId()))) return null;
        if (!permitted(w)) return null;
        return w.matched.getAndIncrement() < 1000 ? w : null;
    }

    private synchronized boolean permitted(Window w) {
        if (window != w) return false;
        if (System.nanoTime() < nextPermissionCheck) return true;
        nextPermissionCheck = System.nanoTime() + Duration.ofSeconds(5).toNanos();
        try {
            var user = users.getById(w.owner);
            if (user != null
                    && user.getStatus() == 1
                    && !Boolean.TRUE.equals(user.getMustChangePassword())
                    && access.isSuperAdmin(w.owner)
                    && access.hasModuleAccess(w.owner, "diagnostics")) return true;
        } catch (RuntimeException ignored) {
        }
        window = null;
        return false;
    }

    private View view(Window w) {
        return new View(
                w != null && clock.instant().isBefore(w.expires) && w.matched.get() < 1000,
                w == null ? null : Long.toString(w.owner),
                w == null ? null : w.expires,
                w == null ? 0 : Math.min(1000, w.matched.get()),
                w == null ? 0 : w.recorded.get(),
                1000,
                w == null ? List.of() : w.routes.stream().sorted().toList(),
                w == null ? null : w.actor,
                w != null && w.errorsOnly);
    }
}
