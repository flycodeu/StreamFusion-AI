package com.streamfusion.platform.auth.config;

import io.swagger.v3.oas.models.Operation;
import io.swagger.v3.oas.models.PathItem;
import io.swagger.v3.oas.models.responses.ApiResponse;
import io.swagger.v3.oas.models.responses.ApiResponses;
import java.util.List;
import org.springdoc.core.customizers.OpenApiCustomizer;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.web.authentication.session.ChangeSessionIdAuthenticationStrategy;
import org.springframework.security.web.authentication.session.CompositeSessionAuthenticationStrategy;
import org.springframework.security.web.authentication.session.SessionAuthenticationStrategy;
import org.springframework.security.web.context.HttpSessionSecurityContextRepository;
import org.springframework.security.web.context.SecurityContextRepository;
import org.springframework.security.web.csrf.CsrfAuthenticationStrategy;
import org.springframework.security.web.csrf.HttpSessionCsrfTokenRepository;
import org.springframework.session.web.http.CookieSerializer;
import org.springframework.session.web.http.DefaultCookieSerializer;

@Configuration(proxyBeanMethods = false)
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
public class AuthWebConfiguration {
    @Bean
    OpenApiCustomizer sessionStatusDocumentation() {
        // This endpoint is handled before MVC so its read-only lookup cannot renew idle time.
        return document ->
                document.path(
                        "/auth/session",
                        new PathItem()
                                .get(
                                        new Operation()
                                                .operationId("sessionStatus")
                                                .tags(List.of("认证与个人资料"))
                                                .summary("只读检查当前会话")
                                                .description(
                                                        "不续期空闲时间，不增加活动记录。使用现有 SF_SESSION Cookie；无需传入账号或会话ID。")
                                                .responses(
                                                        new ApiResponses()
                                                                .addApiResponse(
                                                                        "200",
                                                                        new ApiResponse()
                                                                                .description(
                                                                                        "统一响应 SUCCESS，data 为 null"))
                                                                .addApiResponse(
                                                                        "401",
                                                                        new ApiResponse()
                                                                                .description(
                                                                                        "UNAUTHORIZED，或 SESSION_REPLACED / SESSION_FORCED_LOGOUT；后两者的 data 为安全的会话结束详情"))
                                                                .addApiResponse(
                                                                        "403",
                                                                        new ApiResponse()
                                                                                .description(
                                                                                        "当前IP被限制访问"))
                                                                .addApiResponse(
                                                                        "503",
                                                                        new ApiResponse()
                                                                                .description(
                                                                                        "会话或数据库依赖暂不可用")))));
    }

    @Bean
    HttpSessionCsrfTokenRepository csrfTokenRepository() {
        return new HttpSessionCsrfTokenRepository();
    }

    @Bean
    SecurityContextRepository securityContextRepository() {
        return new HttpSessionSecurityContextRepository();
    }

    @Bean
    SessionAuthenticationStrategy sessionAuthenticationStrategy(
            HttpSessionCsrfTokenRepository csrf) {
        return new CompositeSessionAuthenticationStrategy(
                List.of(
                        new ChangeSessionIdAuthenticationStrategy(),
                        new CsrfAuthenticationStrategy(csrf)));
    }

    @Bean
    CookieSerializer cookieSerializer(AuthProperties properties) {
        var serializer = new DefaultCookieSerializer();
        serializer.setCookieName("SF_SESSION");
        serializer.setCookiePath("/");
        serializer.setUseHttpOnlyCookie(true);
        serializer.setUseSecureCookie(properties.secureCookie());
        serializer.setSameSite("Lax");
        return serializer;
    }
}
