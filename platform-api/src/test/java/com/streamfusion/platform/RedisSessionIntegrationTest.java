package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.doThrow;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.support.IdentitySchema;
import java.net.CookieManager;
import java.net.CookiePolicy;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.Map;
import javax.sql.DataSource;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.boot.test.web.server.LocalServerPort;
import org.springframework.data.redis.RedisConnectionFailureException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.session.data.redis.RedisSessionRepository;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean;

@SpringBootTest(
        webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
        properties = {
            "spring.autoconfigure.exclude=",
            "spring.datasource.url=jdbc:h2:mem:redis_identity;MODE=MySQL;DB_CLOSE_DELAY=-1",
            "spring.session.redis.namespace=streamfusion:isolated-identity-test:session",
            "platform.auth.initial-password=Initial1!"
        })
@ActiveProfiles("test")
@EnabledIfSystemProperty(named = "sf.test.redisSession", matches = "true")
class RedisSessionIntegrationTest {
    @LocalServerPort int port;
    @Autowired DataSource source;
    @Autowired BootstrapService bootstrap;
    @Autowired ObjectMapper json;
    @MockitoSpyBean RedisSessionRepository repository;
    private CookieManager cookies;
    private HttpClient client;

    @BeforeEach
    void prepare() throws Exception {
        IdentitySchema.initialize(source);
        bootstrap.initialize("Admin01", null, "AdminPass1!", null);
        cookies = new CookieManager(null, CookiePolicy.ACCEPT_ALL);
        client =
                HttpClient.newBuilder()
                        .cookieHandler(cookies)
                        .connectTimeout(Duration.ofSeconds(3))
                        .build();
    }

    @Test
    void realHttpUsesRedisCookieCsrfRotationPasswordRevocationAndLogout() throws Exception {
        assertThat(repository).isInstanceOf(RedisSessionRepository.class);
        JsonNode csrf = data(send("GET", "/auth/csrf", null, null));
        String before = cookie();
        var login =
                send(
                        "POST",
                        "/auth/login",
                        Map.of("username", "Admin01", "password", "AdminPass1!"),
                        csrf);
        assertThat(login.statusCode()).isEqualTo(200);
        String authenticatedCookie = cookie();
        assertThat(authenticatedCookie).isNotEqualTo(before);
        assertThat(login.headers().allValues("Set-Cookie").toString())
                .contains("HttpOnly", "SameSite=Lax", "Path=/");
        assertThat(data(send("GET", "/auth/me", null, null)).path("isSuperAdmin").asBoolean())
                .isTrue();
        var missingCsrf = send("PUT", "/auth/me", Map.of("version", "0"), null);
        assertThat(missingCsrf.statusCode()).isEqualTo(403);

        csrf = data(send("GET", "/auth/csrf", null, null));
        var changed =
                send(
                        "PUT",
                        "/auth/password",
                        Map.of("currentPassword", "AdminPass1!", "newPassword", "Changed2@"),
                        csrf);
        assertThat(changed.statusCode()).isEqualTo(200);
        assertThat(withCookie(authenticatedCookie).statusCode()).isEqualTo(401);

        csrf = data(send("GET", "/auth/csrf", null, null));
        assertThat(
                        send(
                                        "POST",
                                        "/auth/login",
                                        Map.of("username", "Admin01", "password", "Changed2@"),
                                        csrf)
                                .statusCode())
                .isEqualTo(200);
        String nextCookie = cookie();
        csrf = data(send("GET", "/auth/csrf", null, null));
        assertThat(send("POST", "/auth/logout", null, csrf).statusCode()).isEqualTo(200);
        assertThat(withCookie(nextCookie).statusCode()).isEqualTo(401);
    }

    @Test
    void redisLogoutDeletionFailureKeepsCookieForRetryAndOnlyRevokesCurrentSession()
            throws Exception {
        JsonNode csrf = login(client);
        String originalCookie = cookie();
        CookieManager otherCookies = new CookieManager(null, CookiePolicy.ACCEPT_ALL);
        HttpClient otherClient = HttpClient.newBuilder().cookieHandler(otherCookies).build();
        login(otherClient);
        String otherCookie = cookie(otherCookies);
        var jdbc = new JdbcTemplate(source);

        doThrow(new RedisConnectionFailureException("synthetic first logout deletion failure"))
                .doCallRealMethod()
                .when(repository)
                .deleteById(anyString());

        var failure = send("POST", "/auth/logout", null, csrf);
        assertThat(failure.statusCode()).isEqualTo(503);
        assertThat(json.readTree(failure.body()).path("code").asText())
                .isEqualTo("DEPENDENCY_UNAVAILABLE");
        assertThat(failure.headers().allValues("Set-Cookie"))
                .noneMatch(value -> value.contains("Max-Age=0"));
        assertThat(cookie()).isEqualTo(originalCookie);
        assertThat(withCookie(originalCookie).statusCode()).isEqualTo(200);
        assertThat(withCookie(otherCookie).statusCode()).isEqualTo(200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='LOGOUT' AND result='SUCCESS'",
                                Integer.class))
                .isZero();
        assertThat(failure.body()).doesNotContain(originalCookie.substring("SF_SESSION=".length()));

        csrf = data(send("GET", "/auth/csrf", null, null));
        assertThat(send("POST", "/auth/logout", null, csrf).statusCode()).isEqualTo(200);
        assertThat(withCookie(originalCookie).statusCode()).isEqualTo(401);
        assertThat(withCookie(otherCookie).statusCode()).isEqualTo(200);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='LOGOUT' AND result='SUCCESS'",
                                Integer.class))
                .isEqualTo(1);
    }

    private JsonNode login(HttpClient browser) throws Exception {
        JsonNode csrf = data(send(browser, "GET", "/auth/csrf", null, null));
        assertThat(
                        send(
                                        browser,
                                        "POST",
                                        "/auth/login",
                                        Map.of("username", "Admin01", "password", "AdminPass1!"),
                                        csrf)
                                .statusCode())
                .isEqualTo(200);
        return data(send(browser, "GET", "/auth/csrf", null, null));
    }

    private HttpResponse<String> send(String method, String path, Object body, JsonNode csrf)
            throws Exception {
        return send(client, method, path, body, csrf);
    }

    private HttpResponse<String> send(
            HttpClient browser, String method, String path, Object body, JsonNode csrf)
            throws Exception {
        var request =
                HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + path))
                        .timeout(Duration.ofSeconds(10));
        if (csrf != null)
            request.header(csrf.path("headerName").asText(), csrf.path("token").asText());
        var publisher =
                body == null
                        ? HttpRequest.BodyPublishers.noBody()
                        : HttpRequest.BodyPublishers.ofString(json.writeValueAsString(body));
        if (body != null) request.header("Content-Type", "application/json");
        return browser.send(
                request.method(method, publisher).build(), HttpResponse.BodyHandlers.ofString());
    }

    private JsonNode data(HttpResponse<String> response) throws Exception {
        assertThat(response.statusCode()).isEqualTo(200);
        return json.readTree(response.body()).path("data");
    }

    private String cookie() {
        return cookie(cookies);
    }

    private String cookie(CookieManager manager) {
        return manager.getCookieStore().getCookies().stream()
                .filter(c -> c.getName().equals("SF_SESSION"))
                .map(c -> c.getName() + "=" + c.getValue())
                .findFirst()
                .orElseThrow();
    }

    private HttpResponse<String> withCookie(String value) throws Exception {
        return HttpClient.newHttpClient()
                .send(
                        HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/auth/me"))
                                .header("Cookie", value)
                                .timeout(Duration.ofSeconds(10))
                                .GET()
                                .build(),
                        HttpResponse.BodyHandlers.ofString());
    }
}
