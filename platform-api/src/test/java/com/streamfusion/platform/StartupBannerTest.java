package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;

import java.io.ByteArrayOutputStream;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;
import org.springframework.boot.ResourceBanner;
import org.springframework.core.env.StandardEnvironment;
import org.springframework.core.io.ClassPathResource;

class StartupBannerTest {
    @Test
    void rendersProjectBrandWithResolvedRuntimeVersions() {
        var output = new ByteArrayOutputStream();
        try (var stream = new PrintStream(output, true, StandardCharsets.UTF_8)) {
            new ResourceBanner(new ClassPathResource("banner.txt"))
                    .printBanner(new StandardEnvironment(), PlatformApplication.class, stream);
        }
        assertThat(output.toString(StandardCharsets.UTF_8))
                .contains("STREAMFUSION AI", "streamfusion-platform-api", "Spring Boot", "Java")
                .doesNotContain("${", ":: Spring Boot ::");
    }
}
