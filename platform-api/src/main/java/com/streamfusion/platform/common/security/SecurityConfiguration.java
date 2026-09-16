package com.streamfusion.platform.common.security;

import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.DispatcherType;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.http.HttpMethod;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.security.web.csrf.CsrfException;

/** Fail-closed foundation. Login and user authorization are intentionally not implemented yet. */
@Configuration
public class SecurityConfiguration {
    @Bean
    SecurityFilterChain securityFilterChain(
            HttpSecurity http,
            ApiErrorWriter writer,
            @Value("${springdoc.api-docs.enabled:true}") boolean docsEnabled)
            throws Exception {
        http.authorizeHttpRequests(
                authorize -> {
                    authorize.dispatcherTypeMatchers(DispatcherType.ERROR).permitAll();
                    authorize.requestMatchers(HttpMethod.GET, "/actuator/health").permitAll();
                    if (docsEnabled) {
                        authorize
                                .requestMatchers(HttpMethod.GET, "/v3/api-docs", "/v3/api-docs/**")
                                .permitAll();
                    }
                    authorize.anyRequest().denyAll();
                });
        http.formLogin(AbstractHttpConfigurer::disable)
                .httpBasic(AbstractHttpConfigurer::disable)
                .logout(AbstractHttpConfigurer::disable)
                .requestCache(AbstractHttpConfigurer::disable)
                .exceptionHandling(
                        errors ->
                                errors.authenticationEntryPoint(
                                                (request, response, exception) ->
                                                        writer.write(
                                                                request,
                                                                response,
                                                                ErrorCode.UNAUTHORIZED))
                                        .accessDeniedHandler(
                                                (request, response, exception) ->
                                                        writer.write(
                                                                request,
                                                                response,
                                                                exception instanceof CsrfException
                                                                                && ApiErrorWriter
                                                                                        .isBusinessRequest(
                                                                                                request)
                                                                        ? ErrorCode.CSRF_INVALID
                                                                        : ErrorCode.FORBIDDEN)));
        // Keep CSRF protection; cookie/JWT/session choices belong to the login design.
        return http.build();
    }
}
