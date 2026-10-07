package com.streamfusion.platform.camera.access.adapter;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.service.CameraSourceRules;
import com.sun.net.httpserver.HttpsConfigurator;
import com.sun.net.httpserver.HttpsServer;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.KeyStore;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManagerFactory;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/**
 * Real local TLS handshakes: isolated test trust does not change system or production TLS settings.
 */
class CameraHttpTlsTest {
    @TempDir static Path directory;
    private static KeyStore matching;
    private static KeyStore mismatching;

    @BeforeAll
    static void certificates() throws Exception {
        matching = certificate("matching", "ip:127.0.0.1");
        mismatching = certificate("mismatching", "dns:camera-fixture.invalid");
    }

    @Test
    void pinnedAddressUsesCertificateValidationAndOriginalHostIdentity() throws Exception {
        exercise(matching, true, null);
        exercise(mismatching, true, "TLS_VALIDATION_FAILED");
    }

    @Test
    void defaultSystemTrustRejectsUntrustedCameraCertificate() throws Exception {
        exercise(matching, false, "TLS_VALIDATION_FAILED");
    }

    private void exercise(KeyStore keys, boolean trustFixture, String failure) throws Exception {
        var managers = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        managers.init(keys, "fixture-only".toCharArray());
        SSLContext serverContext = SSLContext.getInstance("TLS");
        serverContext.init(managers.getKeyManagers(), null, null);
        var server = HttpsServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.setHttpsConfigurator(new HttpsConfigurator(serverContext));
        server.createContext(
                "/",
                exchange -> {
                    byte[] response = "verified".getBytes(StandardCharsets.UTF_8);
                    exchange.sendResponseHeaders(200, response.length);
                    try (var output = exchange.getResponseBody()) {
                        output.write(response);
                    }
                });
        server.start();
        var rules = mock(CameraSourceRules.class);
        when(rules.host(anyString(), anyString()))
                .thenAnswer(invocation -> invocation.getArgument(0));
        var trust = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        trust.init(keys);
        SSLContext clientContext = SSLContext.getInstance("TLS");
        clientContext.init(null, trust.getTrustManagers(), null);
        var transport =
                trustFixture
                        ? new CameraHttpTransport(rules, clientContext.getSocketFactory())
                        : new CameraHttpTransport(rules);
        try {
            var context =
                    new CameraAccessContext(
                            URI.create("https://127.0.0.1:" + server.getAddress().getPort()),
                            "test-only",
                            "",
                            "",
                            554,
                            () -> {});
            if (failure == null)
                assertThat(
                                new String(
                                        transport.open(context).get(context.endpoint()),
                                        StandardCharsets.UTF_8))
                        .isEqualTo("verified");
            else
                assertThatThrownBy(() -> transport.open(context).get(context.endpoint()))
                        .hasMessage(failure);
        } finally {
            transport.close();
            server.stop(0);
        }
    }

    private static KeyStore certificate(String name, String alternativeName) throws Exception {
        Path store = directory.resolve(name + ".p12");
        String executable =
                System.getProperty("os.name").startsWith("Windows") ? "keytool.exe" : "keytool";
        var process =
                new ProcessBuilder(
                                Path.of(System.getProperty("java.home"), "bin", executable)
                                        .toString(),
                                "-genkeypair",
                                "-alias",
                                "fixture",
                                "-keyalg",
                                "RSA",
                                "-keysize",
                                "2048",
                                "-validity",
                                "2",
                                "-dname",
                                "CN=camera-fixture.invalid",
                                "-ext",
                                "SAN=" + alternativeName,
                                "-keystore",
                                store.toString(),
                                "-storetype",
                                "PKCS12",
                                "-storepass",
                                "fixture-only",
                                "-keypass",
                                "fixture-only",
                                "-noprompt")
                        .redirectErrorStream(true)
                        .start();
        if (!process.waitFor(20, TimeUnit.SECONDS)) {
            process.destroyForcibly();
            throw new AssertionError("Local test certificate generation timed out");
        }
        assertThat(process.exitValue()).isZero();
        return KeyStore.getInstance(store.toFile(), "fixture-only".toCharArray());
    }
}
