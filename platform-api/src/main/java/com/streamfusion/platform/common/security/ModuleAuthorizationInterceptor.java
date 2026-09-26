package com.streamfusion.platform.common.security;

import com.streamfusion.platform.auth.controller.AuthController;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;
import org.springframework.web.method.HandlerMethod;
import org.springframework.web.servlet.HandlerInterceptor;

/**
 * 在控制器调用业务服务前，统一校验其所属菜单/功能模块。
 *
 * <p>登录、会话和强制改密仍由认证链处理；此处仅解析模块声明，并检查当前数据库中的授权。
 */
@Component
@RequiredArgsConstructor
public final class ModuleAuthorizationInterceptor implements HandlerInterceptor {
    private final ModuleAuthorizationService authorization;
    private final ApiErrorWriter errors;

    @Override
    public boolean preHandle(
            HttpServletRequest request, HttpServletResponse response, Object handler)
            throws IOException {
        if (!(handler instanceof HandlerMethod method)) {
            return true;
        }
        String module = ModuleRegistry.moduleOf(method);
        if (module == null) {
            // 认证/个人中心由认证控制器负责；其他业务控制器必须显式声明模块。
            // 仅豁免已实现的认证接口；新路径或新方法仍默认拒绝。
            if (isAuthenticationEndpoint(request, method) || isInfrastructure(request)) return true;
            errors.write(request, response, ErrorCode.FORBIDDEN);
            return false;
        }
        if (module.isEmpty()) {
            errors.write(request, response, ErrorCode.FORBIDDEN);
            return false;
        }
        try {
            authorization.authorize(request, module);
            return true;
        } catch (BusinessException ex) {
            errors.write(request, response, ex.code());
            return false;
        }
    }

    private static boolean isAuthenticationEndpoint(
            HttpServletRequest request, HandlerMethod handler) {
        if (handler.getMethod().getDeclaringClass() != AuthController.class) return false;
        String path = request.getRequestURI().substring(request.getContextPath().length());
        String method = request.getMethod();
        boolean read = "GET".equals(method) || "HEAD".equals(method);
        return switch (handler.getMethod().getName()) {
            case "csrf" -> read && path.equals("/auth/csrf");
            case "loginChallenge" -> read && path.equals("/auth/login/challenge");
            case "passwordPolicy" -> read && path.equals("/auth/password-policy");
            case "loginRecords" -> read && path.equals("/auth/login-records/page");
            case "secureLogin" -> method.equals("POST") && path.equals("/auth/login/secure");
            case "me" -> read && path.equals("/auth/me");
            case "update" -> method.equals("PUT") && path.equals("/auth/me");
            case "password" -> method.equals("PUT") && path.equals("/auth/password");
            case "logout" -> method.equals("POST") && path.equals("/auth/logout");
            default -> false;
        };
    }

    private static boolean isInfrastructure(HttpServletRequest request) {
        String path = request.getRequestURI().substring(request.getContextPath().length());
        return path.equals("/actuator/health")
                || path.equals("/error")
                || path.equals("/v3/api-docs")
                || path.startsWith("/v3/api-docs/")
                || path.equals("/swagger-ui.html")
                || path.startsWith("/swagger-ui/");
    }
}
