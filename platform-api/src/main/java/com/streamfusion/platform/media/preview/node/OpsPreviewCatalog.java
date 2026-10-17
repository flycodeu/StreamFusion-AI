package com.streamfusion.platform.media.preview.node;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.media.preview.PreviewFailure;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.ByteBuffer;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.AclEntry;
import java.nio.file.attribute.AclEntryPermission;
import java.nio.file.attribute.AclEntryType;
import java.nio.file.attribute.AclFileAttributeView;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.Flow;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import org.springframework.stereotype.Service;

/** Reads an Ops deployment catalog and hands a selected identity to the existing node verifier. */
@Service
public final class OpsPreviewCatalog {
    private static final Logger logger = LoggerFactory.getLogger(OpsPreviewCatalog.class);
    private static final int MAX_RESPONSE = 131072;
    private final OpsPreviewCatalogProperties ops;
    private final PreviewNodeProperties nodes;
    private final ObjectMapper json;

    public OpsPreviewCatalog(
            OpsPreviewCatalogProperties ops, PreviewNodeProperties nodes, ObjectMapper json) {
        this.ops = ops;
        this.nodes = nodes;
        this.json = json;
    }

    public record Instance(
            String instanceId,
            String name,
            String targetName,
            String targetAddress,
            String installRoot,
            String releaseId,
            String nodeId,
            String controlUri,
            String mediaAddress,
            int maxSources,
            int maxViewers,
            int maxTranscodes) {}

    public record Listing(boolean available, String reasonCode, List<Instance> items) {}

    public record Selection(String instanceId, String releaseId) {}

    public record ZlmInstance(
            String instanceId,
            String name,
            String targetName,
            String targetAddress,
            String installRoot,
            String releaseId,
            String apiUrl,
            String mediaAddress,
            int apiPort,
            int rtcPort,
            int maxSources,
            int maxViewers,
            int maxTranscodes) {}

    public record ZlmListing(boolean available, String reasonCode, List<ZlmInstance> items) {}

    public record ZlmHandoff(ZlmInstance connection, String apiSecret) {}

    public record Prepared(
            PreviewNodeManager.Registration registration, Path tokenFile, Path previousTokenFile) {}

    public boolean configured() {
        return !ops.baseUri().isBlank() && !ops.tokenFile().isBlank();
    }

    public Listing listing() {
        if (!configured()) return new Listing(false, null, List.of());
        try {
            JsonNode items = exchange("GET", "preview-instances", null).path("items");
            if (!items.isArray() || items.size() > 200)
                throw new PreviewFailure("OPS_RESPONSE_INVALID");
            var result = new ArrayList<Instance>();
            for (JsonNode item : items) {
                var value = json.treeToValue(item, Instance.class);
                if (value == null
                        || !identity(value.instanceId())
                        || !identity(value.releaseId())
                        || !identity(value.nodeId())
                        || value.name() == null
                        || value.name().isBlank()
                        || value.name().length() > 80
                        || value.maxSources() < 0
                        || value.maxViewers() < 0
                        || value.maxTranscodes() < 0)
                    throw new PreviewFailure("OPS_RESPONSE_INVALID");
                result.add(value);
            }
            return new Listing(true, null, List.copyOf(result));
        } catch (PreviewFailure failure) {
            return new Listing(true, failure.reason(), List.of());
        } catch (Exception invalid) {
            return new Listing(true, "OPS_RESPONSE_INVALID", List.of());
        }
    }

    public Prepared prepare(Selection selection, PreviewNodeManager manager) {
        if (!configured()) throw new PreviewFailure("OPS_NOT_CONFIGURED");
        if (selection == null
                || !identity(selection.instanceId())
                || !identity(selection.releaseId()))
            throw new PreviewFailure("OPS_SELECTION_INVALID");
        JsonNode handoff =
                exchange(
                        "POST",
                        "preview-instances/" + selection.instanceId() + "/handoff",
                        selection.releaseId());
        if (!selection.instanceId().equals(handoff.path("instanceId").asText())
                || !selection.releaseId().equals(handoff.path("releaseId").asText()))
            throw new PreviewFailure("OPS_RELEASE_CHANGED");
        JsonNode connection = handoff.path("connection");
        String nodeId = connection.path("nodeId").asText();
        String machineToken = handoff.path("machineToken").asText();
        if (!identity(nodeId) || nodeId.equals("local") || !machineToken.matches("[a-f0-9]{64}"))
            throw new PreviewFailure("OPS_RESPONSE_INVALID");
        Path directory = credentialDirectory();
        Path token = directory.resolve(".ops-" + nodeId + "-" + UUID.randomUUID() + ".token");
        PreviewNodeConnection candidate;
        try {
            candidate =
                    new PreviewNodeConnection(
                            nodeId,
                            connection.path("name").asText(),
                            URI.create(connection.path("controlUri").asText()),
                            connection.path("certificateSha256").asText(),
                            token,
                            connection.path("mediaAddress").asText());
        } catch (Exception invalid) {
            throw new PreviewFailure("OPS_RESPONSE_INVALID");
        }
        writeProtectedSecret(token, machineToken);
        try {
            return new Prepared(manager.registration(candidate), token, manager.credential(nodeId));
        } catch (RuntimeException failure) {
            discard(token);
            throw failure;
        }
    }

    public ZlmListing zlmListing() {
        if (!configured()) return new ZlmListing(false, null, List.of());
        try {
            JsonNode items = exchange("GET", "zlm-instances", null).path("items");
            if (!items.isArray() || items.size() > 200)
                throw new PreviewFailure("OPS_RESPONSE_INVALID");
            var result = new ArrayList<ZlmInstance>();
            for (JsonNode item : items) result.add(readZlm(item));
            return new ZlmListing(true, null, List.copyOf(result));
        } catch (PreviewFailure failure) {
            return new ZlmListing(true, failure.reason(), List.of());
        } catch (Exception invalid) {
            return new ZlmListing(true, "OPS_RESPONSE_INVALID", List.of());
        }
    }

    public ZlmHandoff zlmHandoff(Selection selection) {
        if (!configured()) throw new PreviewFailure("OPS_NOT_CONFIGURED");
        if (selection == null
                || !identity(selection.instanceId())
                || !identity(selection.releaseId()))
            throw new PreviewFailure("OPS_SELECTION_INVALID");
        JsonNode handoff =
                exchange(
                        "POST",
                        "zlm-instances/" + selection.instanceId() + "/handoff",
                        selection.releaseId());
        if (!selection.instanceId().equals(handoff.path("instanceId").asText())
                || !selection.releaseId().equals(handoff.path("releaseId").asText()))
            throw new PreviewFailure("OPS_RELEASE_CHANGED");
        ZlmInstance connection = readZlm(handoff.path("connection"));
        String secret = handoff.path("apiSecret").asText();
        if (!selection.instanceId().equals(connection.instanceId())
                || !selection.releaseId().equals(connection.releaseId())
                || secret.isEmpty()
                || secret.length() > 256
                || !secret.equals(secret.strip())
                || secret.chars().anyMatch(ch -> ch < 32 || ch > 126))
            throw new PreviewFailure("OPS_RESPONSE_INVALID");
        return new ZlmHandoff(connection, secret);
    }

    private ZlmInstance readZlm(JsonNode item) {
        try {
            ZlmInstance value = json.treeToValue(item, ZlmInstance.class);
            if (value == null
                    || !identity(value.instanceId())
                    || !identity(value.releaseId())
                    || value.name() == null
                    || value.name().isBlank()
                    || value.name().length() > 80
                    || value.targetName() == null
                    || value.targetName().isBlank()
                    || value.apiUrl() == null
                    || value.mediaAddress() == null
                    || value.maxSources() < 1
                    || value.maxSources() > 64
                    || value.maxViewers() < 1
                    || value.maxViewers() > 64
                    || value.maxTranscodes() != 0) throw new PreviewFailure("OPS_RESPONSE_INVALID");
            URI api = URI.create(value.apiUrl());
            if (!"http".equals(api.getScheme())
                    || api.getHost() == null
                    || api.getPort() != value.apiPort()
                    || api.getRawUserInfo() != null
                    || !api.getRawPath().isEmpty()
                    || api.getRawQuery() != null
                    || api.getRawFragment() != null)
                throw new PreviewFailure("OPS_RESPONSE_INVALID");
            ZlmDirectConnection.privateIpv4(api.getHost());
            ZlmDirectConnection.privateIpv4(value.mediaAddress());
            return value;
        } catch (PreviewFailure known) {
            throw known;
        } catch (Exception invalid) {
            throw new PreviewFailure("OPS_RESPONSE_INVALID");
        }
    }

    public void discard(Path token) {
        try {
            Files.deleteIfExists(token);
        } catch (Exception ignored) {
            // A leftover protected secret is preferable to an untracked replacement.
        }
    }

    public void discardPrevious(Prepared prepared, PreviewNodeManager manager) {
        try {
            Path previous = prepared.previousTokenFile();
            if (previous == null) return;
            synchronized (manager) {
                if (manager.credentialInUse(previous)) return;
                Path directory = credentialDirectory();
                if (previous.getParent().equals(directory)
                        && previous.getFileName()
                                .toString()
                                .matches("\\.ops-[A-Za-z0-9_-]{1,64}-[a-f0-9-]{36}\\.token"))
                    discard(previous);
            }
        } catch (RuntimeException ignored) {
            // Failure to remove a previous protected file must not roll back a committed
            // registration.
        }
    }

    private Path credentialDirectory() {
        try {
            Path path = Path.of(nodes.credentialDirectory());
            if (!path.isAbsolute() || !Files.isDirectory(path, LinkOption.NOFOLLOW_LINKS))
                throw new IllegalArgumentException();
            return path.toRealPath();
        } catch (Exception invalid) {
            throw new PreviewFailure("NODE_CREDENTIAL_DIRECTORY_MISSING");
        }
    }

    public static void writeProtectedSecret(Path path, String token) {
        try {
            if (Files.getFileStore(path.getParent()).supportsFileAttributeView("posix")) {
                try (var output =
                        Files.newByteChannel(
                                path,
                                java.util.Set.of(
                                        StandardOpenOption.CREATE_NEW, StandardOpenOption.WRITE),
                                PosixFilePermissions.asFileAttribute(
                                        PosixFilePermissions.fromString("rw-------")))) {
                    writeFully(output, token);
                }
            } else if (Files.getFileStore(path.getParent()).supportsFileAttributeView("acl")) {
                // The directory can be provisioned by an administrator; the JVM account must own
                // the new secret so it can read it after the create-time ACL is applied.
                var owner =
                        path.getFileSystem()
                                .getUserPrincipalLookupService()
                                .lookupPrincipalByName(System.getProperty("user.name"));
                var ownerOnly =
                        AclEntry.newBuilder()
                                .setType(AclEntryType.ALLOW)
                                .setPrincipal(owner)
                                .setPermissions(java.util.EnumSet.allOf(AclEntryPermission.class))
                                .build();
                var acl =
                        new java.nio.file.attribute.FileAttribute<java.util.List<AclEntry>>() {
                            public String name() {
                                return "acl:acl";
                            }

                            public java.util.List<AclEntry> value() {
                                return java.util.List.of(ownerOnly);
                            }
                        };
                try (var output =
                        Files.newByteChannel(
                                path,
                                java.util.Set.of(
                                        StandardOpenOption.CREATE_NEW, StandardOpenOption.WRITE),
                                acl)) {
                    writeFully(output, token);
                }
            } else {
                throw new IllegalStateException("No protected file permissions available");
            }
        } catch (java.nio.file.FileAlreadyExistsException existing) {
            throw new PreviewFailure("NODE_ALREADY_REGISTERED");
        } catch (Exception invalid) {
            try {
                Files.deleteIfExists(path);
            } catch (Exception ignored) {
            }
            throw new PreviewFailure("NODE_CREDENTIAL_WRITE_FAILED");
        }
    }

    private static void writeFully(java.nio.channels.SeekableByteChannel output, String token)
            throws java.io.IOException {
        ByteBuffer bytes =
                ByteBuffer.wrap(
                        (token + "\n").getBytes(java.nio.charset.StandardCharsets.US_ASCII));
        while (bytes.hasRemaining()) output.write(bytes);
    }

    private JsonNode exchange(String method, String suffix, Object body) {
        URI base = baseUri();
        String token = serviceToken();
        try (var client =
                HttpClient.newBuilder()
                        .connectTimeout(Duration.ofSeconds(3))
                        .followRedirects(HttpClient.Redirect.NEVER)
                        .build()) {
            var request =
                    HttpRequest.newBuilder(base.resolve("/ops/api/v1/platform/" + suffix))
                            .timeout(Duration.ofSeconds(body == null ? 8 : 18))
                            .header("Authorization", "Bearer " + token)
                            .header("Accept", "application/json");
            String traceId = MDC.get("traceId");
            if (traceId != null && traceId.matches("[a-f0-9]{32}"))
                request.header("X-Trace-Id", traceId);
            if (body == null) request.GET();
            else
                request.header("Content-Type", "application/json")
                        .POST(
                                HttpRequest.BodyPublishers.ofByteArray(
                                        json.writeValueAsBytes(
                                                java.util.Map.of("releaseId", (String) body))));
            var response = client.send(request.build(), limitedBody());
            JsonNode envelope = json.readTree(response.body());
            String opsTrace = envelope.path("traceId").asText();
            if (opsTrace.matches("[a-f0-9]{32}") && "POST".equals(method))
                logger.info(
                        "Preview Ops handoff status={} opsTraceId={}",
                        response.statusCode(),
                        opsTrace);
            if (response.statusCode() != 200) {
                if (response.statusCode() == 409) {
                    var code = envelope.path("code").asText();
                    throw new PreviewFailure(
                            switch (code) {
                                case "CATALOG_LIMIT" -> "OPS_CATALOG_LIMIT";
                                case "RELEASE_CHANGED",
                                        "PREVIEW_INSTANCE_REQUIRED",
                                        "ZLM_INSTANCE_REQUIRED" ->
                                        "OPS_RELEASE_CHANGED";
                                case "PREVIEW_HANDOFF_UNAVAILABLE", "ZLM_HANDOFF_UNAVAILABLE" ->
                                        "OPS_HANDOFF_UNAVAILABLE";
                                case "ZLM_SECRET_UNAVAILABLE" -> "OPS_CREDENTIAL_INVALID";
                                default -> "OPS_RESPONSE_INVALID";
                            });
                }
                if (response.statusCode() == 401 || response.statusCode() == 403)
                    throw new PreviewFailure("OPS_AUTH_FAILED");
                throw new PreviewFailure("OPS_UNAVAILABLE");
            }
            if (!"SUCCESS".equals(envelope.path("code").asText())
                    || !envelope.path("data").isObject())
                throw new PreviewFailure("OPS_RESPONSE_INVALID");
            return envelope.path("data");
        } catch (PreviewFailure known) {
            throw known;
        } catch (InterruptedException interrupted) {
            Thread.currentThread().interrupt();
            throw new PreviewFailure("CANCELLED");
        } catch (Exception unavailable) {
            throw new PreviewFailure("OPS_UNAVAILABLE");
        }
    }

    private URI baseUri() {
        try {
            URI base = URI.create(ops.baseUri());
            if (base.getHost() == null
                    || base.getRawUserInfo() != null
                    || !base.getRawPath().isEmpty()
                    || base.getRawQuery() != null
                    || base.getRawFragment() != null
                    || !("https".equals(base.getScheme())
                            || "http".equals(base.getScheme())
                                    && ("127.0.0.1".equals(base.getHost())
                                            || "[::1]".equals(base.getHost()))))
                throw new IllegalArgumentException();
            return base;
        } catch (RuntimeException invalid) {
            throw new PreviewFailure("OPS_CONFIGURATION_INVALID");
        }
    }

    private String serviceToken() {
        try {
            Path path = Path.of(ops.tokenFile());
            Path parent = path.getParent();
            if (!path.isAbsolute()
                    || parent == null
                    || !Files.isDirectory(parent, LinkOption.NOFOLLOW_LINKS)
                    || !Files.isRegularFile(path, LinkOption.NOFOLLOW_LINKS)
                    || Files.size(path) > 256) throw new IllegalArgumentException();
            requireProtectedAccess(parent, false);
            requireProtectedAccess(path, true);
            String value = Files.readString(path).strip();
            if (!value.matches("[A-Za-z0-9_+/=-]{32,128}")) throw new IllegalArgumentException();
            return value;
        } catch (Exception invalid) {
            throw new PreviewFailure("OPS_CREDENTIAL_INVALID");
        }
    }

    private static void requireProtectedAccess(Path path, boolean tokenFile) throws Exception {
        if (Files.getFileStore(path).supportsFileAttributeView("posix")) {
            var permissions = Files.getPosixFilePermissions(path, LinkOption.NOFOLLOW_LINKS);
            if (permissions.stream()
                    .anyMatch(p -> p.name().startsWith("GROUP_") || p.name().startsWith("OTHERS_")))
                throw new IllegalArgumentException();
        } else if (Files.getFileStore(path).supportsFileAttributeView("acl")) {
            var view =
                    Files.getFileAttributeView(
                            path, AclFileAttributeView.class, LinkOption.NOFOLLOW_LINKS);
            var current =
                    path.getFileSystem()
                            .getUserPrincipalLookupService()
                            .lookupPrincipalByName(System.getProperty("user.name"));
            if (view == null || !Files.getOwner(path, LinkOption.NOFOLLOW_LINKS).equals(current))
                throw new IllegalArgumentException();
            boolean currentCanAccess = false;
            for (AclEntry entry : view.getAcl()) {
                if (entry.type() != AclEntryType.ALLOW) continue;
                if (!entry.principal().equals(current)) throw new IllegalArgumentException();
                currentCanAccess |=
                        entry.permissions()
                                .contains(
                                        tokenFile
                                                ? AclEntryPermission.READ_DATA
                                                : AclEntryPermission.ADD_FILE);
            }
            if (!currentCanAccess) throw new IllegalArgumentException();
        } else {
            throw new IllegalArgumentException();
        }
    }

    private static boolean identity(String value) {
        return value != null && value.matches("[A-Za-z0-9_-]{1,64}");
    }

    private static HttpResponse.BodyHandler<byte[]> limitedBody() {
        return ignored ->
                new HttpResponse.BodySubscriber<>() {
                    private final java.io.ByteArrayOutputStream bytes =
                            new java.io.ByteArrayOutputStream();
                    private final CompletableFuture<byte[]> result = new CompletableFuture<>();
                    private Flow.Subscription subscription;

                    public CompletionStage<byte[]> getBody() {
                        return result;
                    }

                    public void onSubscribe(Flow.Subscription value) {
                        subscription = value;
                        value.request(1);
                    }

                    public void onNext(List<ByteBuffer> buffers) {
                        for (ByteBuffer buffer : buffers) {
                            if (bytes.size() + buffer.remaining() > MAX_RESPONSE) {
                                subscription.cancel();
                                result.completeExceptionally(
                                        new IllegalStateException("Ops response too large"));
                                return;
                            }
                            byte[] part = new byte[buffer.remaining()];
                            buffer.get(part);
                            bytes.writeBytes(part);
                        }
                        subscription.request(1);
                    }

                    public void onError(Throwable failure) {
                        result.completeExceptionally(failure);
                    }

                    public void onComplete() {
                        result.complete(bytes.toByteArray());
                    }
                };
    }
}
