package com.streamfusion.platform;

import java.util.TimeZone;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.autoconfigure.security.servlet.UserDetailsServiceAutoConfiguration;

// Authentication is implemented in a later slice; do not create a default user/password.
@SpringBootApplication(exclude = UserDetailsServiceAutoConfiguration.class)
public class PlatformApplication {
    public static void main(String[] args) {
        TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"));
        SpringApplication.run(PlatformApplication.class, args);
    }
}
