package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mockStatic;

import java.util.TimeZone;
import org.junit.jupiter.api.Test;
import org.springframework.boot.SpringApplication;

class ApplicationTimezoneTest {
    @Test
    void mainSetsBeijingTimezoneBeforeStartingSpring() {
        var previous = TimeZone.getDefault();
        try (var spring = mockStatic(SpringApplication.class)) {
            String[] args = {};
            PlatformApplication.main(args);
            assertThat(TimeZone.getDefault().getID()).isEqualTo("Asia/Shanghai");
            spring.verify(() -> SpringApplication.run(PlatformApplication.class, args));
        } finally {
            TimeZone.setDefault(previous);
        }
    }
}
