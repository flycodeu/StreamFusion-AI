package com.streamfusion.platform.common.security;

import jakarta.servlet.RequestDispatcher;
import jakarta.servlet.http.HttpServletRequest;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.http.server.PathContainer;
import org.springframework.stereotype.Component;
import org.springframework.util.AntPathMatcher;
import org.springframework.web.method.HandlerMethod;
import org.springframework.web.servlet.mvc.method.RequestMappingInfo;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

/** Discovers module declarations from registered MVC endpoints, without a second module list. */
@Component
public final class ModuleRegistry {
    private final ObjectProvider<RequestMappingHandlerMapping> mappings;

    public ModuleRegistry(
            @Qualifier("requestMappingHandlerMapping")
                    ObjectProvider<RequestMappingHandlerMapping> mappings) {
        this.mappings = mappings;
    }

    public boolean contains(String module) {
        return module != null
                && !module.isBlank()
                && mappings.getObject().getHandlerMethods().values().stream()
                        .anyMatch(handler -> module.equals(moduleOf(handler)));
    }

    /** Match paths before Spring Security runs so new modules receive the common error contract. */
    public boolean matchesRequest(HttpServletRequest request) {
        Object original = request.getAttribute(RequestDispatcher.ERROR_REQUEST_URI);
        String uri = original instanceof String value ? value : request.getRequestURI();
        String path = uri.substring(request.getContextPath().length());
        return mappings.getObject().getHandlerMethods().entrySet().stream()
                .anyMatch(
                        entry -> {
                            String module = moduleOf(entry.getValue());
                            return module != null
                                    && !module.isBlank()
                                    && matchesPath(entry.getKey(), path);
                        });
    }

    public static String moduleOf(HandlerMethod method) {
        ModuleAccess access = method.getMethodAnnotation(ModuleAccess.class);
        if (access == null) access = method.getBeanType().getAnnotation(ModuleAccess.class);
        return access == null ? null : access.value().strip();
    }

    private static boolean matchesPath(RequestMappingInfo mapping, String path) {
        var patterns = mapping.getPathPatternsCondition();
        if (patterns != null) {
            PathContainer candidate = PathContainer.parsePath(path);
            return patterns.getPatterns().stream().anyMatch(pattern -> pattern.matches(candidate));
        }
        AntPathMatcher matcher = new AntPathMatcher();
        return mapping.getPatternValues().stream()
                .anyMatch(pattern -> matcher.match(pattern, path));
    }
}
