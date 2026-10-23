package com.streamfusion.platform.storage;

import com.streamfusion.platform.common.exception.*;
import com.streamfusion.platform.storage.entity.*;
import com.streamfusion.platform.storage.s3.*;
import java.time.Instant;
import java.util.*;
import lombok.RequiredArgsConstructor;
import org.springframework.http.*;
import org.springframework.stereotype.Service;
import org.springframework.web.servlet.mvc.method.annotation.StreamingResponseBody;

/**
 * Scoped cursor browsing and authenticated, bounded streaming; never materializes a video in RAM.
 */
@Service
@RequiredArgsConstructor
public class StorageObjectService {
    private final StorageConfigurationService configuration;
    private final StorageS3Client s3;
    private final StorageTokens tokens;
    private final StorageAccess access;
    private final Map<Long, Integer> streams = new HashMap<>();

    private synchronized boolean acquire(long actor) {
        int count = streams.getOrDefault(actor, 0);
        if (count >= 4) return false;
        streams.put(actor, count + 1);
        return true;
    }

    private synchronized void release(long actor) {
        int count = streams.getOrDefault(actor, 0);
        if (count <= 1) streams.remove(actor);
        else streams.put(actor, count - 1);
    }

    public Map<String, Object> list(long scopeId, String relativePrefix, String cursor, int limit) {
        var scope = configuration.scope(scopeId);
        var instance = configuration.require(scope.getInstanceId());
        long actor = access.actor();
        if (limit < 1 || limit > 100) throw StorageRules.invalid();
        String prefix = scope.getPrefix() + StorageRules.prefix(relativePrefix);
        StorageRules.key(prefix, true);
        String continuation = null;
        if (cursor != null && !cursor.isEmpty()) {
            var value = tokens.read(cursor, "CURSOR", actor);
            if (!value.path("scopeId").asText().equals(Long.toString(scopeId))
                    || !value.path("scopeVersion").asText().equals(scope.getVersion().toString())
                    || !value.path("instanceVersion")
                            .asText()
                            .equals(instance.getVersion().toString())
                    || !value.path("prefix").asText().equals(prefix)
                    || value.path("limit").asInt() != limit)
                throw BusinessException.error(ErrorCode.STORAGE_OBJECT_CHANGED);
            continuation = value.path("continuation").asText();
        }
        var page = s3.list(instance, scope.getBucket(), prefix, continuation, limit, true);
        var result = new LinkedHashMap<String, Object>();
        result.put("items", page.items().stream().map(this::publicObject).toList());
        result.put("commonPrefixes", page.commonPrefixes());
        result.put("hasMore", page.hasMore());
        result.put("scopeVersion", scope.getVersion().toString());
        result.put("listedAt", Instant.now().toString());
        result.put(
                "nextCursor",
                page.hasMore()
                        ? tokens.create(
                                "CURSOR",
                                actor,
                                Map.of(
                                        "scopeId",
                                        Long.toString(scopeId),
                                        "scopeVersion",
                                        scope.getVersion().toString(),
                                        "instanceVersion",
                                        instance.getVersion().toString(),
                                        "prefix",
                                        prefix,
                                        "limit",
                                        limit,
                                        "continuation",
                                        page.nextCursor()),
                                900)
                        : null);
        return result;
    }

    public Map<String, Object> detail(long scopeId, String key) {
        var scope = configuration.scope(scopeId);
        StorageRules.contains(scope.getPrefix(), key);
        var instance = configuration.require(scope.getInstanceId());
        var object = s3.head(instance, scope.getBucket(), key);
        var result = publicObject(object);
        result.put(
                "objectToken",
                tokens.create(
                        "OBJECT",
                        access.actor(),
                        Map.of(
                                "scopeId",
                                Long.toString(scopeId),
                                "scopeVersion",
                                scope.getVersion().toString(),
                                "instanceVersion",
                                instance.getVersion().toString(),
                                "key",
                                key,
                                "etag",
                                object.etag(),
                                "size",
                                Long.toString(object.size()),
                                "modified",
                                object.lastModified().toString()),
                        300));
        result.put("expiresAt", Instant.now().plusSeconds(300).toString());
        result.put("capability", "READ_ONLY");
        return result;
    }

    public StorageS3Client.ObjectInfo selected(
            StorageScope scope, StorageInstance instance, String key, String token, long actor) {
        StorageRules.contains(scope.getPrefix(), key);
        var value = tokens.read(token, "OBJECT", actor);
        if (!value.path("scopeId").asText().equals(scope.getId().toString())
                || !value.path("scopeVersion").asText().equals(scope.getVersion().toString())
                || !value.path("instanceVersion").asText().equals(instance.getVersion().toString())
                || !value.path("key").asText().equals(key))
            throw BusinessException.error(ErrorCode.STORAGE_OBJECT_CHANGED);
        var object = s3.head(instance, scope.getBucket(), key);
        if (!value.path("etag").asText().equals(object.etag())
                || value.path("size").asLong() != object.size()
                || !value.path("modified").asText().equals(object.lastModified().toString()))
            throw BusinessException.error(ErrorCode.STORAGE_OBJECT_CHANGED);
        return object;
    }

    public ResponseEntity<StreamingResponseBody> content(
            long scopeId, String key, String token, String disposition, String range) {
        long actor = access.actor();
        var scope = configuration.scope(scopeId);
        var instance = configuration.require(scope.getInstanceId());
        var object = selected(scope, instance, key, token, actor);
        if (!Set.of("inline", "attachment").contains(disposition)) throw StorageRules.invalid();
        if (range != null
                && (!range.matches("bytes=(?:[0-9]+-[0-9]*|-[0-9]+)") || range.length() > 80))
            throw BusinessException.error(ErrorCode.STORAGE_RANGE_INVALID);
        if (!acquire(actor)) throw BusinessException.error(ErrorCode.RATE_LIMITED);
        StorageHttpTransport.Response remote;
        try {
            remote = s3.content(instance, scope.getBucket(), object, range);
        } catch (RuntimeException ex) {
            release(actor);
            throw ex;
        }
        if (range != null
                && (remote.status() != 206 || remote.headers().get("content-range") == null)) {
            remote.close();
            release(actor);
            throw BusinessException.error(ErrorCode.STORAGE_RANGE_INVALID);
        }
        try {
            String type =
                    object.contentType() == null
                            ? "application/octet-stream"
                            : object.contentType().split(";", 2)[0].toLowerCase(Locale.ROOT);
            boolean inline =
                    disposition.equals("inline")
                            && Set.of(
                                            "image/jpeg",
                                            "image/png",
                                            "image/webp",
                                            "image/gif",
                                            "video/mp4",
                                            "video/webm")
                                    .contains(type);
            String name = key.substring(key.lastIndexOf('/') + 1);
            if (name.isEmpty()) name = "download";
            var headers = new HttpHeaders();
            headers.setContentType(
                    MediaType.parseMediaType(inline ? type : "application/octet-stream"));
            headers.setContentDisposition(
                    ContentDisposition.builder(inline ? "inline" : "attachment")
                            .filename(name, java.nio.charset.StandardCharsets.UTF_8)
                            .build());
            headers.setCacheControl("private, no-store");
            headers.set("X-Content-Type-Options", "nosniff");
            headers.set("Accept-Ranges", "bytes");
            if (remote.headers().containsKey("content-length"))
                headers.setContentLength(Long.parseLong(remote.headers().get("content-length")));
            if (remote.headers().containsKey("content-range"))
                headers.set("Content-Range", remote.headers().get("content-range"));
            StreamingResponseBody body =
                    output -> {
                        try (remote) {
                            byte[] buffer = new byte[65536];
                            long nextCheck = 0;
                            for (int length; (length = remote.body().read(buffer)) != -1; ) {
                                if (System.nanoTime() >= nextCheck) {
                                    access.background(actor);
                                    if (!configuration
                                                    .require(instance.getId())
                                                    .getVersion()
                                                    .equals(instance.getVersion())
                                            || !configuration
                                                    .scope(scopeId)
                                                    .getVersion()
                                                    .equals(scope.getVersion()))
                                        throw new java.io.IOException("Storage selection changed");
                                    nextCheck =
                                            System.nanoTime()
                                                    + java.util.concurrent.TimeUnit.SECONDS.toNanos(
                                                            2);
                                }
                                output.write(buffer, 0, length);
                            }
                        } finally {
                            release(actor);
                        }
                    };
            return new ResponseEntity<>(body, headers, HttpStatusCode.valueOf(remote.status()));
        } catch (RuntimeException ex) {
            remote.close();
            release(actor);
            throw ex;
        }
    }

    private Map<String, Object> publicObject(StorageS3Client.ObjectInfo object) {
        var view = new LinkedHashMap<String, Object>();
        view.put("objectKey", object.key());
        view.put("name", object.key().substring(object.key().lastIndexOf('/') + 1));
        view.put("sizeBytes", Long.toString(object.size()));
        view.put("etag", object.etag());
        view.put("lastModified", object.lastModified().toString());
        view.put("contentType", object.contentType());
        view.put("objectVersionId", object.objectVersionId());
        return view;
    }
}
