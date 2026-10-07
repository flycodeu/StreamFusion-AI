package com.streamfusion.platform.camera.access.adapter;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import java.util.UUID;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import org.springframework.stereotype.Component;

/** ISC/Artemis resource v2 camera/search; not a universal HikCentral/iVMS/Hik-Connect adapter. */
@Component
public class HikPlatformCameraAccessAdapter implements CameraAccessAdapter {
    private static final String PATH = "/artemis/api/resource/v2/camera/search";
    private final CameraHttpTransport transport;
    private final ObjectMapper json;

    public HikPlatformCameraAccessAdapter(CameraHttpTransport transport, ObjectMapper json) {
        this.transport = transport;
        this.json = json;
    }

    @Override
    public String type() {
        return "HIK_PLATFORM";
    }

    @Override
    public CameraAdapterDescriptor descriptor() {
        return new CameraAdapterDescriptor(
                type(),
                "海康平台",
                "PLATFORM",
                "PLATFORM_APPKEY",
                false,
                40,
                true,
                "/artemis",
                "PLATFORM_HTTP");
    }

    @Override
    public CameraAccessCatalog discover(CameraAccessContext context) {
        var session = transport.open(context);
        var channels = new ArrayList<CameraAccessCatalog.Channel>();
        var identities = new LinkedHashSet<String>();
        var warnings = new LinkedHashSet<String>();
        int page = context.pageNumber(), size = context.pageSize();
        byte[] request;
        try {
            request = json.writeValueAsBytes(Map.of("pageNo", page, "pageSize", size));
        } catch (java.io.IOException ex) {
            throw new IllegalStateException("Cannot encode platform query");
        }
        JsonNode response;
        try {
            response =
                    json.readTree(
                            session.post(
                                    context.endpoint().resolve(PATH),
                                    request,
                                    "application/json",
                                    signedHeaders(
                                            context.username(),
                                            context.password(),
                                            PATH,
                                            String.valueOf(Instant.now().toEpochMilli()),
                                            UUID.randomUUID().toString()),
                                    false));
        } catch (java.io.IOException ex) {
            throw new CameraAdapterException("INVALID_JSON_RESPONSE");
        }
        if (response == null || !"0".equals(response.path("code").asText()))
            throw new CameraAdapterException("PLATFORM_API_REJECTED");
        var data = response.path("data");
        var entries = data.path("list");
        if (!entries.isArray() || !data.path("total").canConvertToLong())
            throw new CameraAdapterException("INVALID_PROTOCOL_RESPONSE");
        long total = data.path("total").asLong();
        if (total < 0 || entries.size() > size)
            throw new CameraAdapterException("INVALID_PROTOCOL_RESPONSE");
        for (JsonNode entry : entries) {
            String id = text(entry, "cameraIndexCode");
            if (id == null) id = text(entry, "indexCode");
            if (id == null || id.length() > 512 || !identities.add(id))
                throw new CameraAdapterException("INVALID_CHANNEL_IDENTITY");
            String name = text(entry, "cameraName");
            if (name == null) name = text(entry, "name");
            // A directory entry proves a channel identity, not an available stream Profile.
            channels.add(new CameraAccessCatalog.Channel(id, name, List.of(), false));
        }
        boolean hasMore = (long) page * size < total;
        if (hasMore && entries.isEmpty()) warnings.add("DIRECTORY_INCOMPLETE");
        return new CameraAccessCatalog(
                type(),
                null,
                channels,
                warnings.isEmpty(),
                List.copyOf(warnings),
                new CameraAccessCatalog.Page(page, size, total, hasMore));
    }

    private String text(JsonNode node, String key) {
        JsonNode value = node.path(key);
        return value.isTextual() ? CameraXml.clean(value.asText()) : null;
    }

    static Map<String, String> signedHeaders(
            String appKey, String appSecret, String path, String timestamp, String nonce) {
        if (appKey == null || appKey.isBlank() || appSecret == null || appSecret.isBlank())
            throw new CameraAdapterException("AUTHENTICATION_FAILED");
        TreeMap<String, String> signed = new TreeMap<>();
        signed.put("x-ca-key", appKey);
        signed.put("x-ca-nonce", nonce);
        signed.put("x-ca-timestamp", timestamp);
        // Matches Artemis SDK: absent Content-MD5 and Date contribute no lines.
        StringBuilder canonical = new StringBuilder("POST\n*/*\napplication/json\n");
        signed.forEach(
                (key, value) -> canonical.append(key).append(':').append(value).append('\n'));
        canonical.append(path);
        try {
            Mac hmac = Mac.getInstance("HmacSHA256");
            hmac.init(new SecretKeySpec(appSecret.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            Map<String, String> headers = new TreeMap<>(signed);
            headers.put(
                    "x-ca-signature",
                    Base64.getEncoder()
                            .encodeToString(
                                    hmac.doFinal(
                                            canonical
                                                    .toString()
                                                    .getBytes(StandardCharsets.UTF_8))));
            headers.put("x-ca-signature-headers", String.join(",", signed.keySet()));
            headers.put("Accept", "*/*");
            return headers;
        } catch (java.security.GeneralSecurityException ex) {
            throw new IllegalStateException("Required signature algorithm unavailable");
        }
    }
}
