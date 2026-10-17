package com.streamfusion.platform.media.preview.node;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import com.streamfusion.platform.media.preview.PreviewProperties;
import com.streamfusion.platform.support.IdentitySchema;
import com.streamfusion.previewnode.PreviewControlTestFixture;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.AclEntry;
import java.nio.file.attribute.AclEntryPermission;
import java.nio.file.attribute.AclEntryType;
import java.nio.file.attribute.AclFileAttributeView;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicReference;
import org.h2.jdbcx.JdbcDataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;

class OpsPreviewCatalogTest {
    @TempDir Path directory;
    private final ObjectMapper json = new ObjectMapper();

    @Test
    void listsPublishedZlmWithoutCredentialAndChecksHandoffIdentity() throws Exception {
        var secrets = Files.createDirectory(directory.resolve("platform-secrets"));
        var serviceToken = directory.resolve("ops-service-token");
        writeProtectedServiceToken(serviceToken);
        var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/ops/api/v1/platform/zlm-instances",
                request -> {
                    String instance =
                            "{\"instanceId\":\"zlm-one\",\"name\":\"ZLM A\","
                                    + "\"targetName\":\"A\",\"targetAddress\":\"10.0.0.10\","
                                    + "\"installRoot\":\"/opt/zlm\",\"releaseId\":\"release-a\","
                                    + "\"apiUrl\":\"http://10.0.0.10:8080\",\"mediaAddress\":\"10.0.0.10\","
                                    + "\"apiPort\":8080,\"rtcPort\":8000,\"maxSources\":64,"
                                    + "\"maxViewers\":64,\"maxTranscodes\":0}";
                    String body;
                    if (request.getRequestURI().getPath().endsWith("/handoff")) {
                        assertThat(request.getRequestMethod()).isEqualTo("POST");
                        assertThat(
                                        new String(
                                                request.getRequestBody().readAllBytes(),
                                                StandardCharsets.UTF_8))
                                .contains("\"releaseId\":\"release-a\"");
                        body =
                                "{\"code\":\"SUCCESS\",\"data\":{\"instanceId\":\"zlm-one\","
                                        + "\"releaseId\":\"release-a\",\"connection\":"
                                        + instance
                                        + ",\"apiSecret\":\"secret one\"}}";
                    } else {
                        assertThat(request.getRequestMethod()).isEqualTo("GET");
                        body = "{\"code\":\"SUCCESS\",\"data\":{\"items\":[" + instance + "]}}";
                    }
                    byte[] encoded = body.getBytes(StandardCharsets.UTF_8);
                    request.sendResponseHeaders(200, encoded.length);
                    try (var output = request.getResponseBody()) {
                        output.write(encoded);
                    }
                });
        server.start();
        try {
            var catalog =
                    new OpsPreviewCatalog(
                            new OpsPreviewCatalogProperties(
                                    "http://127.0.0.1:" + server.getAddress().getPort(),
                                    serviceToken.toString()),
                            new PreviewNodeProperties("", secrets.toString()),
                            json);
            var listing = catalog.zlmListing();
            assertThat(listing.available()).isTrue();
            assertThat(listing.items()).hasSize(1);
            assertThat(json.writeValueAsString(listing)).doesNotContain("secret one");
            var handoff =
                    catalog.zlmHandoff(new OpsPreviewCatalog.Selection("zlm-one", "release-a"));
            assertThat(handoff.connection().apiUrl()).isEqualTo("http://10.0.0.10:8080");
            assertThat(handoff.apiSecret()).isEqualTo("secret one");
            assertThatThrownBy(
                            () ->
                                    catalog.zlmHandoff(
                                            new OpsPreviewCatalog.Selection(
                                                    "zlm-two", "release-a")))
                    .isInstanceOf(PreviewFailure.class)
                    .hasMessage("OPS_RELEASE_CHANGED");
        } finally {
            server.stop(0);
        }
    }

    @Test
    void importsSelectedPublishedNodeWithoutExposingOrEnablingItsMachineCredential()
            throws Exception {
        try (var node = new PreviewControlTestFixture(directory.resolve("node"), "ops-a")) {
            String machineToken = "a".repeat(64);
            var handedToken = new AtomicReference<>(machineToken);
            Files.writeString(node.token, machineToken);
            var secrets = Files.createDirectory(directory.resolve("platform-secrets"));
            var serviceToken = directory.resolve("ops-service-token");
            writeProtectedServiceToken(serviceToken);
            var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
            server.createContext(
                    "/ops/api/v1/platform/preview-instances",
                    request -> {
                        assertThat(request.getRequestHeaders().getFirst("Authorization"))
                                .isEqualTo("Bearer " + "b".repeat(64));
                        assertThat(request.getRequestHeaders().getFirst("Origin")).isNull();
                        String body;
                        if (request.getRequestURI().getPath().endsWith("/handoff")) {
                            assertThat(request.getRequestMethod()).isEqualTo("POST");
                            assertThat(
                                            new String(
                                                    request.getRequestBody().readAllBytes(),
                                                    StandardCharsets.UTF_8))
                                    .contains("\"releaseId\":\"release-a\"");
                            body =
                                    "{\"code\":\"SUCCESS\",\"data\":{\"instanceId\":\"instance-a\",\"releaseId\":\"release-a\",\"connection\":{\"nodeId\":\"ops-a\",\"name\":\"Ops A\",\"controlUri\":\""
                                            + node.origin
                                            + "\",\"certificateSha256\":\""
                                            + node.pin
                                            + "\",\"mediaAddress\":\"127.0.0.1\"},\"machineToken\":\""
                                            + handedToken.get()
                                            + "\"}}";
                        } else {
                            assertThat(request.getRequestMethod()).isEqualTo("GET");
                            body =
                                    "{\"code\":\"SUCCESS\",\"data\":{\"items\":[{\"instanceId\":\"instance-a\",\"name\":\"Ops A\",\"targetName\":\"A\",\"targetAddress\":\"127.0.0.1\",\"installRoot\":\"/opt/preview\",\"releaseId\":\"release-a\",\"nodeId\":\"ops-a\",\"controlUri\":\""
                                            + node.origin
                                            + "\",\"mediaAddress\":\"127.0.0.1\",\"maxSources\":64,\"maxViewers\":64,\"maxTranscodes\":64}]}}";
                        }
                        byte[] encoded = body.getBytes(StandardCharsets.UTF_8);
                        request.getResponseHeaders().add("Content-Type", "application/json");
                        request.sendResponseHeaders(200, encoded.length);
                        try (var output = request.getResponseBody()) {
                            output.write(encoded);
                        }
                    });
            server.start();
            try {
                var catalog =
                        new OpsPreviewCatalog(
                                new OpsPreviewCatalogProperties(
                                        "http://127.0.0.1:" + server.getAddress().getPort(),
                                        serviceToken.toString()),
                                new PreviewNodeProperties("", secrets.toString()),
                                json);
                var repository = repository();
                var manager =
                        new PreviewNodeManager(
                                repository,
                                new PreviewNodeProperties("", secrets.toString()),
                                new PreviewProperties(64, 64),
                                json);

                var listing = catalog.listing();
                assertThat(listing.available()).isTrue();
                assertThat(listing.reasonCode()).isNull();
                assertThat(listing.items()).hasSize(1);
                assertThat(json.writeValueAsString(listing)).doesNotContain(machineToken, node.pin);
                var prepared =
                        catalog.prepare(
                                new OpsPreviewCatalog.Selection("instance-a", "release-a"),
                                manager);
                var result = manager.register(prepared.registration());
                assertThat(result.mode()).isEqualTo("LOCAL");
                assertThat(repository.node("ops-a").mode()).isEqualTo("LOCAL");
                assertThat(Files.readString(prepared.tokenFile()).strip()).isEqualTo(machineToken);
                if (Files.getFileStore(prepared.tokenFile()).supportsFileAttributeView("acl")) {
                    var acl =
                            Files.getFileAttributeView(
                                            prepared.tokenFile(),
                                            java.nio.file.attribute.AclFileAttributeView.class)
                                    .getAcl();
                    var owner = Files.getOwner(prepared.tokenFile());
                    assertThat(acl).allMatch(entry -> entry.principal().equals(owner));
                }
                assertThat(json.writeValueAsString(result)).doesNotContain(machineToken);

                // A failed preflight cannot replace the old credential or database identity.
                handedToken.set("c".repeat(64));
                assertThatThrownBy(
                                () ->
                                        catalog.prepare(
                                                new OpsPreviewCatalog.Selection(
                                                        "instance-a", "release-a"),
                                                manager))
                        .isInstanceOf(PreviewFailure.class);
                assertThat(repository.node("ops-a").connection().tokenFile())
                        .isEqualTo(prepared.tokenFile());
                assertThat(Files.readString(prepared.tokenFile()).strip()).isEqualTo(machineToken);
                try (var entries = Files.list(secrets)) {
                    assertThat(entries.count()).isEqualTo(1);
                }

                // A later valid release can change the node credential without interrupting the old
                // one.
                Files.writeString(node.token, "c".repeat(64));
                var updated =
                        catalog.prepare(
                                new OpsPreviewCatalog.Selection("instance-a", "release-a"),
                                manager);
                manager.register(updated.registration());
                catalog.discardPrevious(updated, manager);
                assertThat(repository.node("ops-a").connection().tokenFile())
                        .isEqualTo(updated.tokenFile());
                assertThat(Files.exists(prepared.tokenFile())).isFalse();
                assertThat(Files.readString(updated.tokenFile()).strip()).isEqualTo("c".repeat(64));

                // A concurrent registry edit fences the import; the newly staged file is discarded.
                var conflicted =
                        catalog.prepare(
                                new OpsPreviewCatalog.Selection("instance-a", "release-a"),
                                manager);
                var current = repository.node("ops-a");
                repository.policy(
                        "ops-a",
                        current.mode(),
                        current.priority(),
                        current.budget(),
                        current.version());
                assertThatThrownBy(() -> manager.register(conflicted.registration()))
                        .isInstanceOf(PreviewFailure.class);
                catalog.discard(conflicted.tokenFile());
                assertThat(Files.exists(conflicted.tokenFile())).isFalse();
                assertThat(Files.exists(updated.tokenFile())).isTrue();
            } finally {
                server.stop(0);
            }
        }
    }

    @Test
    void rejectsRemotePlainHttpAndDoesNotOverwriteExistingCredential() throws Exception {
        var secrets = Files.createDirectory(directory.resolve("platform-secrets"));
        var token = directory.resolve("ops-service-token");
        writeProtectedServiceToken(token);
        var insecure =
                new OpsPreviewCatalog(
                        new OpsPreviewCatalogProperties("http://192.0.2.25:8090", token.toString()),
                        new PreviewNodeProperties("", secrets.toString()),
                        json);
        assertThat(insecure.listing().reasonCode()).isEqualTo("OPS_CONFIGURATION_INVALID");
        assertThatThrownBy(
                        () ->
                                insecure.prepare(
                                        new OpsPreviewCatalog.Selection("../escape", "release-a"),
                                        null))
                .isInstanceOf(PreviewFailure.class)
                .hasMessage("OPS_SELECTION_INVALID");
    }

    @Test
    void distinguishesCatalogLimitFromStaleReleaseAndUnavailableHandoff() throws Exception {
        var secrets = Files.createDirectory(directory.resolve("platform-secrets"));
        var serviceToken = directory.resolve("ops-service-token");
        writeProtectedServiceToken(serviceToken);
        var result = new AtomicReference<>("CATALOG_LIMIT");
        var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
                "/ops/api/v1/platform/preview-instances",
                request -> {
                    byte[] body =
                            ("{\"code\":\"" + result.get() + "\",\"data\":null}")
                                    .getBytes(StandardCharsets.UTF_8);
                    request.sendResponseHeaders(409, body.length);
                    try (var output = request.getResponseBody()) {
                        output.write(body);
                    }
                });
        server.start();
        try {
            var catalog =
                    new OpsPreviewCatalog(
                            new OpsPreviewCatalogProperties(
                                    "http://127.0.0.1:" + server.getAddress().getPort(),
                                    serviceToken.toString()),
                            new PreviewNodeProperties("", secrets.toString()),
                            json);
            assertThat(catalog.listing().reasonCode()).isEqualTo("OPS_CATALOG_LIMIT");
            result.set("RELEASE_CHANGED");
            assertThatThrownBy(
                            () ->
                                    catalog.prepare(
                                            new OpsPreviewCatalog.Selection(
                                                    "instance-a", "release-a"),
                                            null))
                    .isInstanceOf(PreviewFailure.class)
                    .hasMessage("OPS_RELEASE_CHANGED");
            result.set("PREVIEW_HANDOFF_UNAVAILABLE");
            assertThatThrownBy(
                            () ->
                                    catalog.prepare(
                                            new OpsPreviewCatalog.Selection(
                                                    "instance-a", "release-a"),
                                            null))
                    .isInstanceOf(PreviewFailure.class)
                    .hasMessage("OPS_HANDOFF_UNAVAILABLE");
            try (var entries = Files.list(secrets)) {
                assertThat(entries).isEmpty();
            }
        } finally {
            server.stop(0);
        }
    }

    private PreviewNodeRepository repository() throws Exception {
        var dataSource = new JdbcDataSource();
        dataSource.setURL("jdbc:h2:mem:" + UUID.randomUUID() + ";MODE=MySQL;DB_CLOSE_DELAY=-1");
        IdentitySchema.initialize(dataSource);
        return new PreviewNodeRepository(new JdbcTemplate(dataSource));
    }

    @Test
    void rejectsServiceTokenReadableByAnotherWindowsPrincipal() throws Exception {
        var token = directory.resolve("ops-service-token");
        writeProtectedServiceToken(token);
        if (!Files.getFileStore(token).supportsFileAttributeView("acl")) return;
        var view = Files.getFileAttributeView(token, AclFileAttributeView.class);
        var everyone =
                token.getFileSystem()
                        .getUserPrincipalLookupService()
                        .lookupPrincipalByName("Everyone");
        var broad =
                AclEntry.newBuilder()
                        .setType(AclEntryType.ALLOW)
                        .setPrincipal(everyone)
                        .setPermissions(AclEntryPermission.READ_DATA)
                        .build();
        var entries = new java.util.ArrayList<>(view.getAcl());
        entries.add(broad);
        view.setAcl(entries);
        var catalog =
                new OpsPreviewCatalog(
                        new OpsPreviewCatalogProperties("http://127.0.0.1:9", token.toString()),
                        new PreviewNodeProperties("", ""),
                        json);
        assertThat(catalog.listing().reasonCode()).isEqualTo("OPS_CREDENTIAL_INVALID");
    }

    @Test
    void rejectsServiceTokenWhenWindowsParentAllowsUsersToWrite() throws Exception {
        var token = directory.resolve("ops-service-token");
        writeProtectedServiceToken(token);
        if (!Files.getFileStore(directory).supportsFileAttributeView("acl")) return;
        var view = Files.getFileAttributeView(directory, AclFileAttributeView.class);
        var users =
                directory
                        .getFileSystem()
                        .getUserPrincipalLookupService()
                        .lookupPrincipalByName("BUILTIN\\Users");
        var broad =
                AclEntry.newBuilder()
                        .setType(AclEntryType.ALLOW)
                        .setPrincipal(users)
                        .setPermissions(AclEntryPermission.ADD_FILE)
                        .build();
        var entries = new java.util.ArrayList<>(view.getAcl());
        entries.add(broad);
        view.setAcl(entries);
        var catalog =
                new OpsPreviewCatalog(
                        new OpsPreviewCatalogProperties("http://127.0.0.1:9", token.toString()),
                        new PreviewNodeProperties("", ""),
                        json);
        assertThat(catalog.listing().reasonCode()).isEqualTo("OPS_CREDENTIAL_INVALID");
    }

    private static void writeProtectedServiceToken(Path path) throws Exception {
        Files.writeString(path, "b".repeat(64));
        if (Files.getFileStore(path).supportsFileAttributeView("posix")) {
            Files.setPosixFilePermissions(
                    path.getParent(), PosixFilePermissions.fromString("rwx------"));
            Files.setPosixFilePermissions(path, PosixFilePermissions.fromString("rw-------"));
        } else if (Files.getFileStore(path).supportsFileAttributeView("acl")) {
            var owner = Files.getOwner(path);
            var ownerOnly =
                    AclEntry.newBuilder()
                            .setType(AclEntryType.ALLOW)
                            .setPrincipal(owner)
                            .setPermissions(java.util.EnumSet.allOf(AclEntryPermission.class))
                            .build();
            Files.getFileAttributeView(path.getParent(), AclFileAttributeView.class)
                    .setAcl(java.util.List.of(ownerOnly));
            Files.getFileAttributeView(path, AclFileAttributeView.class)
                    .setAcl(java.util.List.of(ownerOnly));
        }
    }
}
