package com.streamfusion.platform.common.security;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.web.ApiError;
import jakarta.servlet.DispatcherType;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.http.HttpMethod;
import org.springframework.http.MediaType;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.web.SecurityFilterChain;

/** Fail-closed foundation. Login and user authorization are intentionally not implemented yet. */
@Configuration
public class SecurityConfiguration {
    @Bean
    SecurityFilterChain securityFilterChain(
            HttpSecurity http,
            ObjectMapper mapper,
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
                                                        writeError(mapper, response, 401))
                                        .accessDeniedHandler(
                                                (request, response, exception) ->
                                                        writeError(mapper, response, 403)));
        // Keep CSRF protection; cookie/JWT/session choices belong to the login design.
        return http.build();
    }

    private static void writeError(ObjectMapper mapper, HttpServletResponse response, int status)
            throws IOException {
        response.setStatus(status);
        response.setContentType(MediaType.APPLICATION_JSON_VALUE);
        response.setCharacterEncoding("UTF-8");
        mapper.writeValue(response.getOutputStream(), ApiError.fromStatus(status));
    }
}
