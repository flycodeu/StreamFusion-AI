package com.streamfusion.previewnode;

import static org.mockito.Mockito.*;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.control.PreviewControlTls;
import com.streamfusion.platform.media.preview.execution.PreviewExecutor;
import com.streamfusion.platform.media.preview.node.PreviewNodeClient;
import java.net.InetSocketAddress;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.security.MessageDigest;
import java.time.Clock;
import java.util.HexFormat;
import java.util.List;
import java.util.concurrent.TimeUnit;

/** Real TLS server/client around an isolated execution double; never uses business services. */
public final class PreviewControlTestFixture implements AutoCloseable {
    public final ObjectMapper json = new ObjectMapper().findAndRegisterModules();
    public final PreviewExecutor core = mock(PreviewExecutor.class);
    public final PreviewNodeJournal journal;
    public final PreviewNodeService service;
    public final PreviewNodeServer server;
    public final Path token;
    public final String pin;
    public final URI origin;

    public PreviewControlTestFixture(Path directory) throws Exception {
        this(directory, "test-a");
    }

    public PreviewControlTestFixture(Path directory, String nodeId) throws Exception {
        Files.createDirectories(directory);
        token = directory.resolve("machine-token");
        Files.writeString(token, "a".repeat(48));
        Path password = directory.resolve("tls-password");
        Files.writeString(password, "b".repeat(48));
        Path store = directory.resolve("identity.p12");
        var process =
                new ProcessBuilder(
                                Path.of(System.getProperty("java.home"), "bin", "keytool")
                                        .toString(),
                                "-genkeypair",
                                "-alias",
                                "node",
                                "-keyalg",
                                "EC",
                                "-groupname",
                                "secp256r1",
                                "-dname",
                                "CN=localhost",
                                "-ext",
                                "SAN=DNS:localhost,IP:127.0.0.1",
                                "-validity",
                                "2",
                                "-storetype",
                                "PKCS12",
                                "-keystore",
                                store.toString(),
                                "-storepass:file",
                                password.toString(),
                                "-noprompt")
                        .redirectErrorStream(true)
                        .redirectOutput(directory.resolve("keytool.log").toFile())
                        .start();
        if (!process.waitFor(20, TimeUnit.SECONDS) || process.exitValue() != 0) {
            process.destroyForcibly();
            throw new IllegalStateException("Test TLS identity generation failed");
        }
        var keys = KeyStore.getInstance("PKCS12");
        try (var input = Files.newInputStream(store)) {
            keys.load(input, "b".repeat(48).toCharArray());
        }
        pin =
                HexFormat.of()
                        .formatHex(
                                MessageDigest.getInstance("SHA-256")
                                        .digest(keys.getCertificate("node").getEncoded()));
        journal = new PreviewNodeJournal(directory.resolve("journal"), json);
        when(core.start(anyString(), anyList(), any(), any()))
                .thenReturn(new PreviewExecutor.Playback("H264", false));
        when(core.missingSourceViewers(anyString(), any())).thenReturn(List.of());
        service =
                new PreviewNodeService(
                        nodeId,
                        "c".repeat(64),
                        64,
                        64,
                        core,
                        journal,
                        new PreviewNodeSourcePolicy(List.of("192.0.2.10:554")),
                        json,
                        Clock.systemUTC(),
                        () -> {});
        service.tick();
        server =
                new PreviewNodeServer(
                        new InetSocketAddress("127.0.0.1", 0),
                        PreviewControlTls.server(store, password),
                        token,
                        service,
                        json);
        server.start();
        origin = URI.create("https://127.0.0.1:" + server.port());
    }

    public PreviewNodeClient client() {
        return new PreviewNodeClient(origin, pin, token, json);
    }

    public void close() {
        server.close();
        journal.close();
    }
}
