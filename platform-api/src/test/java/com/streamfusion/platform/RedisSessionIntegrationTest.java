package com.streamfusion.platform;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.ArgumentMatchers.anyString;
import static org.mockito.Mockito.doThrow;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.auth.service.BootstrapService;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.platform.support.SecureLoginSupport;
import java.net.CookieManager;
import java.net.CookiePolicy;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Base64;
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
import org.springframework.session.Session;
import org.springframework.session.SessionRepository;
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
        var login = secureLogin(client, "Admin01", "AdminPass1!", csrf);
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
        assertThat(secureLogin(client, "Admin01", "Changed2@", csrf).statusCode()).isEqualTo(200);
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
        var jdbc = new JdbcTemplate(source);
        jdbc.update(
                "INSERT INTO sys_user(id,username,nickname,password,status,must_change_password) SELECT 811,'OtherAccount','另一账号',password,1,FALSE FROM sys_user WHERE username='Admin01'");
        login(otherClient, "OtherAccount");
        String otherCookie = cookie(otherCookies);

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
        return login(browser, "Admin01");
    }

    private JsonNode login(HttpClient browser, String username) throws Exception {
        JsonNode csrf = data(send(browser, "GET", "/auth/csrf", null, null));
        assertThat(secureLogin(browser, username, "AdminPass1!", csrf).statusCode()).isEqualTo(200);
        return data(send(browser, "GET", "/auth/csrf", null, null));
    }

    @Test
    void statusPollingDoesNotRenewRedisIdleTimeAndReportsReplacementToTheOldCookie()
            throws Exception {
        JsonNode previousCsrf = login(client);
        String previous = cookie();
        String previousId = decodedId(previous);
        var before = ((Session) repository.findById(previousId)).getLastAccessedTime();
        var jdbc = new JdbcTemplate(source);
        var activity =
                jdbc.queryForObject(
                        "SELECT last_activity_at FROM sys_login_record", java.sql.Timestamp.class);
        for (int attempt = 0; attempt < 3; attempt++) {
            assertThat(withCookie(previous, "/auth/session").statusCode()).isEqualTo(200);
        }
        assertThat(((Session) repository.findById(previousId)).getLastAccessedTime())
                .isEqualTo(before);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT last_activity_at FROM sys_login_record",
                                java.sql.Timestamp.class))
                .isEqualTo(activity);
        CookieManager newerCookies = new CookieManager(null, CookiePolicy.ACCEPT_ALL);
        HttpClient newer = HttpClient.newBuilder().cookieHandler(newerCookies).build();
        login(newer);
        for (int attempt = 0; attempt < 2; attempt++) {
            var response = withCookie(previous, "/auth/session");
            assertThat(response.statusCode()).isEqualTo(401);
            assertThat(json.readTree(response.body()).path("code").asText())
                    .isEqualTo("SESSION_REPLACED");
            assertThat(json.readTree(response.body()).path("data").path("sourceIp").asText())
                    .isEqualTo("127.0.0.1");
            assertThat(response.body()).doesNotContain(previousId);
        }
        assertThat(((Session) repository.findById(previousId)).getLastAccessedTime())
                .isEqualTo(before);
        String currentCookie = cookie(newerCookies);
        var oldRequest = withCookie(previous, "/auth/me");
        assertThat(oldRequest.statusCode()).isEqualTo(401);
        assertThat(oldRequest.headers().allValues("Set-Cookie"))
                .noneMatch(value -> value.startsWith("SF_SESSION="));
        assertThat(repository.findById(previousId)).isNull();
        var lateRequest = withCookie(previous, "/auth/me");
        assertThat(lateRequest.statusCode()).isEqualTo(401);
        assertThat(lateRequest.headers().allValues("Set-Cookie"))
                .noneMatch(value -> value.startsWith("SF_SESSION="));
        var lateWrite =
                HttpClient.newHttpClient()
                        .send(
                                HttpRequest.newBuilder(
                                                URI.create("http://127.0.0.1:" + port + "/user"))
                                        .header("Cookie", previous)
                                        .header(
                                                previousCsrf.path("headerName").asText(),
                                                previousCsrf.path("token").asText())
                                        .header("Content-Type", "application/json")
                                        .POST(HttpRequest.BodyPublishers.ofString("{}"))
                                        .timeout(Duration.ofSeconds(10))
                                        .build(),
                                HttpResponse.BodyHandlers.ofString());
        assertThat(lateWrite.statusCode()).isEqualTo(403);
        assertThat(json.readTree(lateWrite.body()).path("code").asText()).isEqualTo("CSRF_INVALID");
        assertThat(lateWrite.headers().allValues("Set-Cookie"))
                .noneMatch(value -> value.startsWith("SF_SESSION="));
        assertThat(withCookie(currentCookie, "/auth/session").statusCode()).isEqualTo(200);
        var expired = repository.findById(decodedId(currentCookie));
        ((Session) expired).setLastAccessedTime(java.time.Instant.now().minus(Duration.ofHours(1)));
        // FlushMode.IMMEDIATE expires the Redis key during the timestamp change itself.
        assertThat(withCookie(currentCookie, "/auth/session").statusCode()).isEqualTo(401);
    }

    @Test
    void correctingAWrongPasswordReusesTheSameAnonymousCsrfSession() throws Exception {
        JsonNode csrf = data(send("GET", "/auth/csrf", null, null));
        String anonymousCookie = cookie();
        assertThat(secureLogin(client, "Admin01", "WrongPass1!", csrf).statusCode()).isEqualTo(401);
        assertThat(cookie()).isEqualTo(anonymousCookie);
        var jdbc = new JdbcTemplate(source);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT failed_login_count FROM sys_user WHERE username='Admin01'",
                                Integer.class))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='LOGIN' AND result='FAILURE'",
                                Integer.class))
                .isEqualTo(1);
        assertThat(secureLogin(client, "Admin01", "AdminPass1!", csrf).statusCode()).isEqualTo(200);
        assertThat(cookie()).isNotEqualTo(anonymousCookie);
        assertThat(send("GET", "/auth/session", null, null).statusCode()).isEqualTo(200);
    }

    @Test
    void aReplacedBrowserCanPrepareCsrfAndLogInAgainOnItsFirstAttempt() throws Exception {
        login(client);
        String replacedCookie = cookie();
        CookieManager newerCookies = new CookieManager(null, CookiePolicy.ACCEPT_ALL);
        HttpClient newer = HttpClient.newBuilder().cookieHandler(newerCookies).build();
        login(newer);
        var status = send("GET", "/auth/session", null, null);
        assertThat(status.statusCode()).isEqualTo(401);
        assertThat(json.readTree(status.body()).path("code").asText())
                .isEqualTo("SESSION_REPLACED");
        assertThat(cookie()).isEqualTo(replacedCookie);
        JsonNode csrf = data(send("GET", "/auth/csrf", null, null));
        assertThat(cookie()).isNotEqualTo(replacedCookie);
        assertThat(repository.findById(decodedId(replacedCookie))).isNull();
        assertThat(send("GET", "/auth/me", null, null).statusCode()).isEqualTo(401);
        assertThat(secureLogin(client, "Admin01", "AdminPass1!", csrf).statusCode()).isEqualTo(200);
        assertThat(send("GET", "/auth/session", null, null).statusCode()).isEqualTo(200);
        assertThat(
                        json.readTree(send(newer, "GET", "/auth/session", null, null).body())
                                .path("code")
                                .asText())
                .isEqualTo("SESSION_REPLACED");
    }

    @Test
    @SuppressWarnings("unchecked")
    void failureOfTheFinalRedisSaveRollsBackReplacementBeforeSendingTheNewCookie()
            throws Exception {
        login(client);
        String previous = cookie();
        CookieManager candidateCookies = new CookieManager(null, CookiePolicy.ACCEPT_ALL);
        HttpClient candidate = HttpClient.newBuilder().cookieHandler(candidateCookies).build();
        JsonNode csrf = data(send(candidate, "GET", "/auth/csrf", null, null));
        String anonymousCookie = cookie(candidateCookies);
        var envelope = loginEnvelope(candidate, "Admin01", "AdminPass1!");
        var sessions = (SessionRepository<Session>) (SessionRepository<?>) repository;
        doThrow(new RedisConnectionFailureException("synthetic final login save failure"))
                .doCallRealMethod()
                .when(sessions)
                .save(any(Session.class));
        var result = send(candidate, "POST", "/auth/login/secure", envelope, csrf);
        assertThat(result.statusCode()).isEqualTo(503);
        assertThat(json.readTree(result.body()).path("code").asText())
                .isEqualTo("DEPENDENCY_UNAVAILABLE");
        assertThat(result.headers().allValues("Set-Cookie"))
                .noneMatch(value -> value.startsWith("SF_SESSION="));
        assertThat(cookie(candidateCookies)).isEqualTo(anonymousCookie);
        assertThat(withCookie(previous, "/auth/session").statusCode()).isEqualTo(200);
        var jdbc = new JdbcTemplate(source);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT session_version FROM sys_user WHERE username='Admin01'",
                                Long.class))
                .isEqualTo(1);
        assertThat(jdbc.queryForObject("SELECT COUNT(*) FROM sys_login_record", Integer.class))
                .isEqualTo(1);
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_login_record WHERE end_reason IS NOT NULL",
                                Integer.class))
                .isZero();
        assertThat(
                        jdbc.queryForObject(
                                "SELECT COUNT(*) FROM sys_operation_log WHERE action='LOGIN' AND result='SUCCESS'",
                                Integer.class))
                .isEqualTo(1);
    }

    private HttpResponse<String> secureLogin(
            HttpClient browser, String username, String password, JsonNode csrf) throws Exception {
        return send(
                browser,
                "POST",
                "/auth/login/secure",
                loginEnvelope(browser, username, password),
                csrf);
    }

    private Map<String, String> loginEnvelope(HttpClient browser, String username, String password)
            throws Exception {
        JsonNode challenge = data(send(browser, "GET", "/auth/login/challenge", null, null));
        return SecureLoginSupport.encrypt(json, challenge, username, password);
    }

    private static String decodedId(String cookie) {
        return new String(
                Base64.getDecoder()
                        .decode(cookie.substring("SF_SESSION=".length()).replace("\"", "")),
                StandardCharsets.UTF_8);
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
        return withCookie(value, "/auth/me");
    }

    private HttpResponse<String> withCookie(String value, String path) throws Exception {
        return HttpClient.newHttpClient()
                .send(
                        HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + path))
                                .header("Cookie", value)
                                .timeout(Duration.ofSeconds(10))
                                .GET()
                                .build(),
                        HttpResponse.BodyHandlers.ofString());
    }
}
