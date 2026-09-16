package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.springframework.security.test.web.servlet.request.SecurityMockMvcRequestPostProcessors.user;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.web.ApiErrorWriter;
import jakarta.servlet.DispatcherType;
import jakarta.servlet.RequestDispatcher;
import java.net.URI;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.slf4j.MDC;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.autoconfigure.web.servlet.AutoConfigureMockMvc;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.test.context.TestConfiguration;
import org.springframework.boot.test.web.client.TestRestTemplate;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Import;
import org.springframework.core.annotation.Order;
import org.springframework.http.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import org.springframework.security.access.prepost.PreAuthorize;
import org.springframework.security.config.annotation.method.configuration.EnableMethodSecurity;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.web.bind.annotation.*;

@SpringBootTest(webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
@AutoConfigureMockMvc
@ActiveProfiles("test")
@Import(ApiIntegrationTest.Fixtures.class)
class ApiIntegrationTest {
    private static final String TRACE = "c".repeat(32);
    private final TestRestTemplate client;
    private final MockMvc mvc;
    private final JdbcTemplate jdbc;
    private final ApiErrorWriter writer;
    private final ObjectMapper mapper;

    @Autowired
    ApiIntegrationTest(
            TestRestTemplate client,
            MockMvc mvc,
            JdbcTemplate jdbc,
            ApiErrorWriter writer,
            ObjectMapper mapper) {
        this.client = client;
        this.mvc = mvc;
        this.jdbc = jdbc;
        this.writer = writer;
        this.mapper = mapper;
    }

    @TestConfiguration(proxyBeanMethods = false)
    @EnableMethodSecurity
    static class Fixtures {
        @Bean
        @Order(0)
        SecurityFilterChain fixtureSecurity(HttpSecurity http) throws Exception {
            return http.securityMatcher("/api/v1/test-only/**")
                    .authorizeHttpRequests(auth -> auth.anyRequest().permitAll())
                    .build();
        }

        @Bean
        TestEndpoints testEndpoints(FailingService service) {
            return new TestEndpoints(service);
        }

        @Bean
        FailingService failingService(JdbcTemplate jdbc) {
            return new FailingService(jdbc);
        }
    }

    static class FailingService {
        private final JdbcTemplate jdbc;

        FailingService(JdbcTemplate jdbc) {
            this.jdbc = jdbc;
        }

        @Transactional
        public void fail() {
            jdbc.update("INSERT INTO integration_probe (kind) VALUES ('business')");
            jdbc.update("INSERT INTO integration_probe (kind) VALUES ('audit')");
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
    }

    @RestController
    @RequestMapping("/api/v1/test-only")
    static class TestEndpoints {
        private final FailingService service;

        TestEndpoints(FailingService service) {
            this.service = service;
        }

        @GetMapping("/object")
        R<Map<String, String>> object() {
            return R.success(Map.of("id", "9007199254740993"));
        }

        @PostMapping("/created")
        ResponseEntity<R<String>> created() {
            return ResponseEntity.created(URI.create("/api/v1/test-only/object"))
                    .eTag("\"7\"")
                    .body(R.success("value"));
        }

        @GetMapping("/empty")
        R<Void> empty() {
            return R.success();
        }

        @GetMapping("/binding")
        R<Integer> binding(@RequestParam int count) {
            return R.success(count);
        }

        @GetMapping("/fields")
        R<Input> fields(@ModelAttribute Input input) {
            return R.success(input);
        }

        @GetMapping("/failure")
        R<Void> failure() {
            throw new IllegalStateException("password=dummy-private");
        }

        @GetMapping("/transaction")
        R<Void> transaction() {
            service.fail();
            return R.success();
        }

        @GetMapping("/details")
        R<Void> details() {
            throw BusinessException.error(
                    ErrorCode.VALIDATION_ERROR,
                    new ValidationDetails(List.of(ValidationDetails.FieldError.invalid("count"))));
        }

        @GetMapping("/bad-details")
        R<Void> badDetails() {
            throw BusinessException.error(ErrorCode.CONFLICT, Map.of("password", "dummy-private"));
        }

        @GetMapping("/restricted")
        @PreAuthorize("hasAuthority('test:write')")
        public R<Void> restricted() {
            return R.success();
        }
    }

    public record Input(int count) {}

    private ResponseEntity<JsonNode> fetch(String path, HttpMethod method, Object body) {
        HttpHeaders headers = new HttpHeaders();
        headers.set("X-Trace-Id", TRACE);
        headers.setContentType(MediaType.APPLICATION_JSON);
        return client.exchange(
                "/api/v1/test-only" + path,
                method,
                new HttpEntity<>(body, headers),
                JsonNode.class);
    }

    private void envelope(ResponseEntity<JsonNode> response, int status, String code) {
        assertThat(response.getStatusCode().value()).isEqualTo(status);
        assertThat(response.getHeaders().getFirst("X-Trace-Id")).isEqualTo(TRACE);
        assertThat(response.getHeaders().getCacheControl()).isEqualTo("no-store");
        JsonNode body = response.getBody();
        assertThat(body).isNotNull();
        assertThat(body.size()).isEqualTo(5);
        assertThat(body.has("msg")).isTrue();
        assertThat(body.has("message")).isFalse();
        assertThat(body.has("data")).isTrue();
        assertThat(body.get("code").asText()).isEqualTo(code);
        assertThat(body.get("traceId").asText()).isEqualTo(TRACE);
        assertThat(Instant.parse(body.get("timestamp").asText())).isNotNull();
        assertThat(body.toString()).doesNotContain("dummy-private", "IllegalStateException");
    }

    @Test
    void successPreservesDataAndExplicitNull() {
        var response = fetch("/object", HttpMethod.GET, null);
        envelope(response, 200, "SUCCESS");
        assertThat(response.getBody().path("data").path("id").asText())
                .isEqualTo("9007199254740993");
        response = fetch("/empty", HttpMethod.GET, null);
        envelope(response, 200, "SUCCESS");
        assertThat(response.getBody().get("data").isNull()).isTrue();
    }

    @Test
    void methodFailurePreservesAllowHeader() {
        var response = fetch("/created", HttpMethod.GET, null);
        envelope(response, 405, "METHOD_NOT_ALLOWED");
        assertThat(response.getHeaders().getAllow()).contains(HttpMethod.POST);
    }

    @Test
    void creationPreservesStatusAndHeaders() throws Exception {
        mvc.perform(
                        org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post(
                                        "/api/v1/test-only/created")
                                .with(
                                        org.springframework.security.test.web.servlet.request
                                                .SecurityMockMvcRequestPostProcessors.csrf()))
                .andExpect(status().isCreated())
                .andExpect(header().string("Location", "/api/v1/test-only/object"))
                .andExpect(header().string("ETag", "\"7\""))
                .andExpect(jsonPath("$.data").value("value"));
    }

    @ParameterizedTest
    @CsvSource({
        "/binding?count=dummy-private,400,VALIDATION_ERROR",
        "/failure,500,INTERNAL_ERROR",
        "/details,400,VALIDATION_ERROR",
        "/bad-details,500,INTERNAL_ERROR",
        "/missing,404,NOT_FOUND"
    })
    void mvcFailuresUseEnvelope(String path, int status, String code) {
        envelope(fetch(path, HttpMethod.GET, null), status, code);
    }

    @Test
    void bindingDetailsNeverExposeRejectedValues() {
        var response = fetch("/fields?count=dummy-private", HttpMethod.GET, null);
        envelope(response, 400, "VALIDATION_ERROR");
        assertThat(
                        response.getBody()
                                .path("data")
                                .path("fieldErrors")
                                .get(0)
                                .path("field")
                                .asText())
                .isEqualTo("count");
    }

    @Test
    void businessThrowRollsBackBothWritesThroughRealTransactionProxy() {
        jdbc.execute("CREATE TABLE IF NOT EXISTS integration_probe (kind VARCHAR(32))");
        jdbc.update("DELETE FROM integration_probe");
        envelope(fetch("/transaction", HttpMethod.GET, null), 409, "VERSION_CONFLICT");
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM integration_probe", Integer.class))
                .isZero();
    }

    @Test
    void methodAuthorizationIsNotAnInternalError() throws Exception {
        mvc.perform(get("/api/v1/test-only/restricted").with(user("reader")))
                .andExpect(status().isForbidden())
                .andExpect(jsonPath("$.code").value("FORBIDDEN"));
        mvc.perform(get("/api/v1/test-only/restricted"))
                .andExpect(status().isUnauthorized())
                .andExpect(jsonPath("$.code").value("UNAUTHORIZED"));
    }

    @Test
    void errorDispatchUsesOriginalPathAndNeverAcceptsSuccessfulErrorStatus() throws Exception {
        mvc.perform(
                        get("/error")
                                .with(
                                        request -> {
                                            request.setDispatcherType(DispatcherType.ERROR);
                                            return request;
                                        })
                                .requestAttr(
                                        RequestDispatcher.ERROR_REQUEST_URI,
                                        "/api/v1/test-only/object")
                                .requestAttr(RequestDispatcher.ERROR_STATUS_CODE, 503))
                .andExpect(status().isServiceUnavailable())
                .andExpect(jsonPath("$.code").value("DEPENDENCY_UNAVAILABLE"))
                .andExpect(jsonPath("$.msg").exists())
                .andExpect(jsonPath("$.message").doesNotExist());
        mvc.perform(
                        get("/error")
                                .with(
                                        request -> {
                                            request.setDispatcherType(DispatcherType.ERROR);
                                            return request;
                                        })
                                .requestAttr(RequestDispatcher.ERROR_STATUS_CODE, 200))
                .andExpect(status().isNotFound());
    }

    @Test
    void writerDoesNotOverwriteCommittedResponseOrWriteHeadBody() throws Exception {
        var request = new MockHttpServletRequest("GET", "/api/v1/users");
        var response = new MockHttpServletResponse();
        response.getWriter().write("already sent");
        response.flushBuffer();
        writer.write(request, response, ErrorCode.FORBIDDEN);
        assertThat(response.getContentAsString()).isEqualTo("already sent");
        try (var ignored = MDC.putCloseable("traceId", TRACE)) {
            request.setMethod("HEAD");
            var head = new MockHttpServletResponse();
            writer.write(request, head, ErrorCode.UNAUTHORIZED);
            assertThat(head.getStatus()).isEqualTo(401);
            assertThat(head.getContentAsByteArray()).isEmpty();
            var configuredMapper =
                    mapper.copy()
                            .setSerializationInclusion(
                                    com.fasterxml.jackson.annotation.JsonInclude.Include.NON_NULL);
            assertThat(
                            configuredMapper
                                    .readTree(configuredMapper.writeValueAsBytes(R.success()))
                                    .has("data"))
                    .isTrue();
            assertThatThrownBy(() -> R.error(ErrorCode.CONFLICT, Map.of("secret", "hidden")))
                    .isInstanceOf(IllegalArgumentException.class);
            assertThatThrownBy(() -> R.success(R.success()))
                    .isInstanceOf(IllegalArgumentException.class);
        }
    }
}
