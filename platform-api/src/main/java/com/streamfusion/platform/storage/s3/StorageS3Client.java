package com.streamfusion.platform.storage.s3;

import com.streamfusion.platform.storage.StorageSecrets;
import com.streamfusion.platform.storage.entity.StorageInstance;
import java.io.ByteArrayInputStream;
import java.net.URI;
import java.net.URLDecoder;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.time.format.DateTimeFormatter;
import java.util.*;
import javax.xml.parsers.DocumentBuilderFactory;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;
import org.w3c.dom.Element;

/** Scoped object operations; manual mutations never use a recursive remote delete. */
@Component
@RequiredArgsConstructor
public class StorageS3Client {
    private final StorageHttpTransport transport;
    private final StorageSecrets secrets;

    public record ObjectInfo(
            String key,
            long size,
            String etag,
            Instant lastModified,
            String contentType,
            String objectVersionId) {}

    public record ObjectPage(
            List<ObjectInfo> items,
            List<String> commonPrefixes,
            String nextCursor,
            boolean hasMore) {}

    public record BucketProtection(
            boolean versioning,
            boolean objectLock,
            boolean lifecycle,
            boolean verified,
            String reason) {}

    public record PutGrant(String url, Map<String, String> headers) {}

    public PutGrant presignedPut(
            StorageInstance instance,
            String bucket,
            String key,
            long size,
            String contentType,
            String checksumBase64,
            int expiresSeconds,
            Instant signedAt) {
        URI endpoint = URI.create(instance.getEndpoint());
        if (!"https".equalsIgnoreCase(endpoint.getScheme()))
            throw new StorageRemoteException("STORAGE_HTTPS_REQUIRED", 503);
        var headers = new LinkedHashMap<String, String>();
        headers.put("if-none-match", "*");
        headers.put("content-length", Long.toString(size));
        headers.put("content-type", contentType);
        headers.put("x-amz-checksum-sha256", checksumBase64);
        var credentials = secrets.read(instance);
        String url =
                S3Signature.presignedUrl(
                        instance.getEndpoint(),
                        bucket,
                        key,
                        instance.getRegion(),
                        credentials.path("accessKey").asText(),
                        credentials.path("secretKey").asText(),
                        expiresSeconds,
                        headers,
                        signedAt);
        return new PutGrant(url, Map.copyOf(headers));
    }

    public ObjectPage list(
            StorageInstance instance,
            String bucket,
            String prefix,
            String continuation,
            int limit,
            boolean directories) {
        var query = new LinkedHashMap<String, String>();
        query.put("list-type", "2");
        query.put("encoding-type", "url");
        query.put("prefix", prefix);
        query.put("max-keys", Integer.toString(limit));
        if (directories) query.put("delimiter", "/");
        if (continuation != null && !continuation.isEmpty())
            query.put("continuation-token", continuation);
        try (var response = request(instance, "GET", bucket, "", query, Map.of(), false)) {
            require(response, 200);
            Element root = xml(response.bytes(4 * 1024 * 1024));
            var items = new ArrayList<ObjectInfo>();
            var common = new ArrayList<String>();
            var nodes = root.getElementsByTagName("Contents");
            for (int i = 0; i < nodes.getLength(); i++) {
                Element entry = (Element) nodes.item(i);
                String key = decode(text(entry, "Key"));
                if (!key.startsWith(prefix))
                    throw new StorageRemoteException("STORAGE_RESPONSE_INVALID", 200);
                long size = Long.parseLong(text(entry, "Size"));
                if (size < 0) throw new StorageRemoteException("STORAGE_RESPONSE_INVALID", 200);
                items.add(
                        new ObjectInfo(
                                key,
                                size,
                                text(entry, "ETag"),
                                Instant.parse(text(entry, "LastModified")),
                                null,
                                null));
            }
            nodes = root.getElementsByTagName("CommonPrefixes");
            for (int i = 0; i < nodes.getLength(); i++) {
                String value = decode(text((Element) nodes.item(i), "Prefix"));
                if (!value.startsWith(prefix))
                    throw new StorageRemoteException("STORAGE_RESPONSE_INVALID", 200);
                common.add(value);
            }
            if (items.size() + common.size() > limit)
                throw new StorageRemoteException("STORAGE_RESPONSE_LIMIT", 200);
            boolean more = "true".equals(text(root, "IsTruncated"));
            String next = text(root, "NextContinuationToken");
            if (more && (next.isEmpty() || next.equals(continuation)))
                throw new StorageRemoteException("STORAGE_CURSOR_INVALID", 200);
            return new ObjectPage(
                    List.copyOf(items), List.copyOf(common), more ? next : null, more);
        } catch (IllegalArgumentException ex) {
            throw new StorageRemoteException("STORAGE_RESPONSE_INVALID", 0);
        }
    }

    public ObjectInfo head(StorageInstance instance, String bucket, String key) {
        try (var response = request(instance, "HEAD", bucket, key, Map.of(), Map.of(), false)) {
            require(response, 200);
            try {
                String etag = response.headers().get("etag"),
                        modified = response.headers().get("last-modified");
                long size = Long.parseLong(response.headers().get("content-length"));
                if (etag == null || modified == null || size < 0)
                    throw new IllegalArgumentException();
                return new ObjectInfo(
                        key,
                        size,
                        etag,
                        java.time.ZonedDateTime.parse(
                                        modified, DateTimeFormatter.RFC_1123_DATE_TIME)
                                .toInstant(),
                        response.headers().getOrDefault("content-type", "application/octet-stream"),
                        response.headers().get("x-amz-version-id"));
            } catch (RuntimeException ex) {
                throw new StorageRemoteException("STORAGE_RESPONSE_INVALID", 200);
            }
        }
    }

    public StorageHttpTransport.Response content(
            StorageInstance instance, String bucket, ObjectInfo object, String range) {
        var headers = new LinkedHashMap<String, String>();
        headers.put("if-match", object.etag());
        if (range != null) headers.put("range", range);
        var response =
                request(
                        instance,
                        "GET",
                        bucket,
                        object.key(),
                        object.objectVersionId() == null
                                ? Map.of()
                                : Map.of("versionId", object.objectVersionId()),
                        headers,
                        true);
        if (response.status() != 200 && response.status() != 206) {
            int status = response.status();
            response.close();
            throw new StorageRemoteException(
                    status == 412 ? "STORAGE_OBJECT_CHANGED" : "STORAGE_GET_FAILED", status);
        }
        return response;
    }

    public BucketProtection protection(StorageInstance instance, String bucket) {
        try {
            Element versioning = configuration(instance, bucket, "versioning", Set.of(404));
            Element lock = configuration(instance, bucket, "object-lock", Set.of(404));
            Element lifecycle = configuration(instance, bucket, "lifecycle", Set.of(404));
            return new BucketProtection(
                    versioning != null
                            && Set.of("Enabled", "Suspended").contains(text(versioning, "Status")),
                    lock != null && "Enabled".equals(text(lock, "ObjectLockEnabled")),
                    lifecycle != null && lifecycle.getElementsByTagName("Rule").getLength() > 0,
                    false,
                    "STORAGE_DELETE_IDENTITY_UNVERIFIED");
        } catch (StorageRemoteException ex) {
            return new BucketProtection(false, false, false, false, ex.reason());
        }
    }

    public void putIfAbsent(
            StorageInstance instance, String bucket, String key, byte[] body, String contentType) {
        try (var response =
                request(
                        instance,
                        "PUT",
                        bucket,
                        key,
                        Map.of(),
                        Map.of("if-none-match", "*", "content-type", contentType),
                        body,
                        false)) {
            if (response.status() == 412)
                throw new StorageRemoteException("STORAGE_FILE_EXISTS", 409);
            require(response, 200);
        }
    }

    public void deleteSelected(StorageInstance instance, String bucket, ObjectInfo object) {
        if (object.key().endsWith("/") || object.etag() == null || object.etag().isBlank())
            throw new StorageRemoteException("STORAGE_DELETE_TARGET_INVALID", 400);
        deleteConditional(instance, bucket, object);
    }

    /** SeaweedFS S3 removes/demotes this marker without recursively deleting child objects. */
    public void deleteEmptyMarker(StorageInstance instance, String bucket, ObjectInfo object) {
        if (!object.key().endsWith("/")
                || object.size() != 0
                || object.etag() == null
                || object.etag().isBlank())
            throw new StorageRemoteException("STORAGE_DELETE_TARGET_INVALID", 400);
        var remaining = list(instance, bucket, object.key(), null, 2, false);
        if (remaining.hasMore()
                || !remaining.items().stream().allMatch(item -> item.key().equals(object.key())))
            throw new StorageRemoteException("STORAGE_FOLDER_CHANGED", 409);
        deleteConditional(instance, bucket, object);
    }

    private void deleteConditional(StorageInstance instance, String bucket, ObjectInfo object) {
        String expectedEtag = object.etag();
        // SeaweedFS 4.48 HEAD uses filer.ETag for an unmarked, chunkless directory, while
        // conditional DELETE uses getObjectETag and returns the empty opaque tag for that entry.
        // Keep an exact If-Match content condition; never replace it with '*' or omit it.
        if (object.key().endsWith("/")
                && object.size() == 0
                && "application/x-directory".equalsIgnoreCase(object.contentType())
                && "\"d41d8cd98f00b204e9800998ecf8427e-0\"".equals(expectedEtag))
            expectedEtag = "\"\"";
        try (var response =
                request(
                        instance,
                        "DELETE",
                        bucket,
                        object.key(),
                        object.objectVersionId() == null
                                ? Map.of()
                                : Map.of("versionId", object.objectVersionId()),
                        Map.of("if-match", expectedEtag),
                        false)) {
            require(response, 204);
        }
    }

    private Element configuration(
            StorageInstance instance, String bucket, String name, Set<Integer> absent) {
        try (var response =
                request(instance, "GET", bucket, "", Map.of(name, ""), Map.of(), false)) {
            if (absent.contains(response.status())) return null;
            require(response, 200);
            return xml(response.bytes(65536));
        }
    }

    private StorageHttpTransport.Response request(
            StorageInstance instance,
            String method,
            String bucket,
            String key,
            Map<String, String> query,
            Map<String, String> extra,
            boolean stream) {
        return request(instance, method, bucket, key, query, extra, new byte[0], stream);
    }

    private StorageHttpTransport.Response request(
            StorageInstance instance,
            String method,
            String bucket,
            String key,
            Map<String, String> query,
            Map<String, String> extra,
            byte[] body,
            boolean stream) {
        URI endpoint = URI.create(instance.getEndpoint());
        String path =
                "/"
                        + S3Signature.encode(bucket, false)
                        + (key.isEmpty() ? "" : "/" + S3Signature.encode(key, true));
        String encodedQuery = S3Signature.query(query);
        var credentials = secrets.read(instance);
        var headers =
                S3Signature.sign(
                        method,
                        path,
                        encodedQuery,
                        endpoint.getRawAuthority(),
                        instance.getRegion(),
                        credentials.path("accessKey").asText(),
                        credentials.path("secretKey").asText(),
                        body,
                        extra,
                        Instant.now());
        return transport.exchange(
                endpoint,
                instance.getNetworkPolicyKey(),
                method,
                path + (encodedQuery.isEmpty() ? "" : "?" + encodedQuery),
                headers,
                body,
                stream);
    }

    static Element xml(byte[] bytes) {
        try {
            var factory = DocumentBuilderFactory.newInstance();
            factory.setFeature("http://apache.org/xml/features/disallow-doctype-decl", true);
            factory.setFeature("http://xml.org/sax/features/external-general-entities", false);
            factory.setFeature("http://xml.org/sax/features/external-parameter-entities", false);
            factory.setXIncludeAware(false);
            factory.setExpandEntityReferences(false);
            return factory.newDocumentBuilder()
                    .parse(new ByteArrayInputStream(bytes))
                    .getDocumentElement();
        } catch (Exception ex) {
            throw new StorageRemoteException("STORAGE_XML_INVALID", 0);
        }
    }

    private static String text(Element element, String name) {
        var nodes = element.getElementsByTagName(name);
        return nodes.getLength() == 0 ? "" : nodes.item(0).getTextContent();
    }

    private static String decode(String value) {
        return URLDecoder.decode(value.replace("+", "%2B"), StandardCharsets.UTF_8);
    }

    private static void require(StorageHttpTransport.Response response, int expected) {
        if (response.status() != expected)
            throw new StorageRemoteException(
                    switch (response.status()) {
                        case 401, 403 -> "STORAGE_AUTH_FAILED";
                        case 404 -> "STORAGE_OBJECT_NOT_FOUND";
                        case 412 -> "STORAGE_OBJECT_CHANGED";
                        default -> "STORAGE_UPSTREAM_ERROR";
                    },
                    response.status());
    }
}
