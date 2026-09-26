package com.streamfusion.platform;

import io.swagger.v3.oas.annotations.OpenAPIDefinition;
import io.swagger.v3.oas.annotations.info.Info;
import java.util.Arrays;
import java.util.TimeZone;
import java.util.stream.Stream;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.WebApplicationType;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.autoconfigure.security.servlet.UserDetailsServiceAutoConfiguration;

// Administrator credentials are persisted only through an explicit non-Web bootstrap.
@SpringBootApplication(exclude = UserDetailsServiceAutoConfiguration.class)
@OpenAPIDefinition(info = @Info(title = "StreamFusion 管理接口", version = "0.1"))
public class PlatformApplication {
    public static void main(String[] args) {
        TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"));
        if (Arrays.asList(args).contains("--bootstrap-admin")) {
            var application = new SpringApplication(PlatformApplication.class);
            application.setWebApplicationType(WebApplicationType.NONE);
            String[] bootstrapArgs =
                    Stream.concat(
                                    Arrays.stream(args)
                                            .filter(
                                                    value ->
                                                            !value.equals("--bootstrap-admin")
                                                                    && !value.startsWith(
                                                                            "--spring.main.web-application-type=")
                                                                    && !value.startsWith(
                                                                            "--platform.bootstrap.enabled=")),
                                    Stream.of(
                                            "--platform.bootstrap.enabled=true",
                                            "--spring.main.web-application-type=none"))
                            .toArray(String[]::new);
            try (var context = application.run(bootstrapArgs)) {
                SpringApplication.exit(context);
            }
        } else {
            SpringApplication.run(PlatformApplication.class, args);
        }
    }
}
