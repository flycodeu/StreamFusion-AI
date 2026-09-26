package com.streamfusion.platform.apidocs;

import static org.assertj.core.api.Assertions.assertThat;

import com.streamfusion.platform.apidocs.controller.ApiDocsController;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.slf4j.MDC;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class ApiDocsControllerTest {
    @ParameterizedTest
    @CsvSource({"true,true,true", "true,false,false", "false,true,false", "false,false,false"})
    void requiresBothConfiguredDocumentationEndpoints(
            boolean apiDocsEnabled, boolean swaggerUiEnabled, boolean available) {
        new ApplicationContextRunner()
                .withBean(ApiDocsController.class)
                .withPropertyValues(
                        "springdoc.api-docs.enabled=" + apiDocsEnabled,
                        "springdoc.swagger-ui.enabled=" + swaggerUiEnabled)
                .run(
                        context -> {
                            try (var trace = MDC.putCloseable("traceId", "a".repeat(32))) {
                                assertThat(
                                                context.getBean(ApiDocsController.class)
                                                        .status()
                                                        .data()
                                                        .enabled())
                                        .isEqualTo(available);
                            }
                        });
    }
}
