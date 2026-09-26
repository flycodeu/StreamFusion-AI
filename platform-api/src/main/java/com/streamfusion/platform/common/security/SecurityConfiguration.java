package com.streamfusion.platform.common.security;

import com.streamfusion.platform.auth.security.SessionGuardFilter;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import com.streamfusion.platform.loginrecord.service.LoginRecordService;
import jakarta.servlet.DispatcherType;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.security.web.access.AccessDeniedHandler;
import org.springframework.security.web.context.SecurityContextHolderFilter;
import org.springframework.security.web.context.SecurityContextRepository;
import org.springframework.security.web.csrf.CsrfException;
import org.springframework.security.web.csrf.HttpSessionCsrfTokenRepository;

/** Authenticates API requests; module authorization is applied by the MVC interceptor. */
@Configuration
@ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
public class SecurityConfiguration {
    @Bean
    SecurityFilterChain securityFilterChain(
            HttpSecurity http,
            ApiErrorWriter writer,
            CurrentUserService current,
            LoginRecordService loginRecords,
            SecurityContextRepository contexts,
            HttpSessionCsrfTokenRepository csrf,
            @Value("${springdoc.api-docs.enabled:true}") boolean docsEnabled)
            throws Exception {
        http.authorizeHttpRequests(
                authorize -> {
                    authorize.dispatcherTypeMatchers(DispatcherType.ERROR).permitAll();
                    authorize.requestMatchers("/actuator/health").permitAll();
                    authorize
                            .requestMatchers(
                                    "/auth/csrf",
                                    "/auth/login",
                                    "/auth/login/challenge",
                                    "/auth/login/secure")
                            .permitAll();
                    if (docsEnabled) {
                        authorize
                                .requestMatchers(
                                        "/v3/api-docs",
                                        "/v3/api-docs/**",
                                        "/swagger-ui.html",
                                        "/swagger-ui/**")
                                .permitAll();
                    }
                    // TraceFilter recognizes registered @ModuleAccess paths before this chain.
                    // The error-contract boundary also preserves existing business URL behavior.
                    authorize.requestMatchers(ApiErrorWriter::isBusinessRequest).authenticated();
                    // MVC still rejects undeclared business handlers; unknown roots stay denied.
                    authorize.anyRequest().denyAll();
                });
        http.formLogin(AbstractHttpConfigurer::disable)
                .httpBasic(AbstractHttpConfigurer::disable)
                .logout(AbstractHttpConfigurer::disable)
                .requestCache(AbstractHttpConfigurer::disable)
                .exceptionHandling(
                        errors -> {
                            errors.authenticationEntryPoint(
                                    (request, response, exception) ->
                                            writer.write(
                                                    request, response, ErrorCode.UNAUTHORIZED));
                            errors.accessDeniedHandler(accessDeniedHandler(writer));
                        });
        http.securityContext(
                config -> config.securityContextRepository(contexts).requireExplicitSave(true));
        http.csrf(config -> config.csrfTokenRepository(csrf));
        http.addFilterAfter(
                new SessionGuardFilter(current, writer, loginRecords),
                SecurityContextHolderFilter.class);
        return http.build();
    }

    private static AccessDeniedHandler accessDeniedHandler(ApiErrorWriter writer) {
        return (request, response, exception) -> {
            ErrorCode code =
                    exception instanceof CsrfException && ApiErrorWriter.isBusinessRequest(request)
                            ? ErrorCode.CSRF_INVALID
                            : ErrorCode.FORBIDDEN;
            writer.write(request, response, code);
        };
    }
}
