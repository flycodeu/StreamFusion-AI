package com.streamfusion.platform.camera.access.adapter;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.time.Instant;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.springframework.stereotype.Component;

/** Shared Artemis transport. Platform errors never become empty successful directories. */
@Component
public class HikPlatformApi {
    public static final int STATUS_BATCH_SIZE = 100;
    private final CameraHttpTransport transport;
    private final ObjectMapper json;

    public HikPlatformApi(CameraHttpTransport transport, ObjectMapper json) {
        this.transport = transport;
        this.json = json;
    }

    public JsonNode post(CameraAccessContext context, String path, Object body) {
        context.checkAuthorization().run();
        try {
            var response =
                    json.readTree(
                            transport
                                    .open(context)
                                    .post(
                                            context.endpoint().resolve(path),
                                            json.writeValueAsBytes(body),
                                            "application/json",
                                            HikPlatformCameraAccessAdapter.signedHeaders(
                                                    context.username(),
                                                    context.password(),
                                                    path,
                                                    Long.toString(Instant.now().toEpochMilli()),
                                                    UUID.randomUUID().toString()),
                                            false));
            context.checkAuthorization().run();
            if (response == null || !"0".equals(response.path("code").asText()))
                throw new CameraAdapterException("PLATFORM_API_REJECTED");
            if (response.path("data").isMissingNode() || response.path("data").isNull())
                throw new CameraAdapterException("INVALID_PROTOCOL_RESPONSE");
            return response.path("data");
        } catch (java.io.IOException ex) {
            throw new CameraAdapterException("INVALID_JSON_RESPONSE");
        }
    }

    public JsonNode root(CameraAccessContext context) {
        return post(context, "/artemis/api/resource/v1/regions/root", Map.of("treeCode", "0"));
    }

    public JsonNode regions(CameraAccessContext context, String parent, int page, int size) {
        requirePage(page, size);
        requireCode(parent);
        return page(
                post(
                        context,
                        "/artemis/api/irds/v2/region/nodesByParams",
                        Map.of(
                                "parentIndexCodes",
                                List.of(parent),
                                "isSubRegion",
                                false,
                                "resourceType",
                                "region",
                                "pageNo",
                                page,
                                "pageSize",
                                size,
                                "cascadeFlag",
                                0)),
                size);
    }

    public JsonNode cameras(
            CameraAccessContext context, String region, boolean descendants, int page, int size) {
        requirePage(page, size);
        requireCode(region);
        return page(
                post(
                        context,
                        "/artemis/api/resource/v2/camera/search",
                        Map.of(
                                "regionIndexCodes",
                                List.of(region),
                                "isSubRegion",
                                descendants,
                                "pageNo",
                                page,
                                "pageSize",
                                size)),
                size);
    }

    public int online(CameraAccessContext context, String cameraCode) {
        var result = onlineBatch(context, List.of(cameraCode)).get(cameraCode);
        if (result.value() == null) throw new CameraAdapterException(result.reasonCode());
        return result.value();
    }

    public record OnlineStatus(Integer value, String reasonCode) {}

    /** One bounded request; missing/unknown entries never become offline facts. */
    public Map<String, OnlineStatus> onlineBatch(CameraAccessContext context, List<String> codes) {
        if (codes == null || codes.isEmpty() || codes.size() > STATUS_BATCH_SIZE)
            throw new CameraAdapterException("PLATFORM_STATUS_BATCH_INVALID");
        codes.forEach(HikPlatformApi::requireCode);
        if (new HashSet<>(codes).size() != codes.size())
            throw new CameraAdapterException("PLATFORM_STATUS_BATCH_INVALID");
        var data =
                page(
                        post(
                                context,
                                "/artemis/api/nms/v1/online/camera/get",
                                Map.of("indexCodes", codes, "pageNo", 1, "pageSize", codes.size())),
                        codes.size());
        var results = new LinkedHashMap<String, OnlineStatus>();
        codes.forEach(code -> results.put(code, new OnlineStatus(null, "PLATFORM_STATUS_MISSING")));
        var seen = new HashSet<String>();
        for (var item : data.path("list")) {
            String code = item.path("indexCode").asText("");
            String alternative = item.path("cameraIndexCode").asText("");
            if (code.isEmpty()) code = alternative;
            if (!alternative.isEmpty() && !code.equals(alternative))
                throw new CameraAdapterException("INVALID_PROTOCOL_RESPONSE");
            if (!results.containsKey(code)) continue;
            if (!seen.add(code)) {
                results.put(code, new OnlineStatus(null, "PLATFORM_STATUS_UNKNOWN"));
                continue;
            }
            var value = item.path("online");
            Integer online = null;
            if (value.isIntegralNumber()
                    && value.canConvertToInt()
                    && (value.intValue() == 0 || value.intValue() == 1)) online = value.intValue();
            if (value.isTextual()
                    && ("0".equals(value.textValue()) || "1".equals(value.textValue())))
                online = Integer.parseInt(value.textValue());
            results.put(
                    code,
                    new OnlineStatus(online, online == null ? "PLATFORM_STATUS_UNKNOWN" : null));
        }
        return Map.copyOf(results);
    }

    private static JsonNode page(JsonNode data, int size) {
        if (!data.path("list").isArray()
                || data.path("list").size() > size
                || !data.path("total").isIntegralNumber()
                || !data.path("total").canConvertToLong()
                || data.path("total").asLong() < 0)
            throw new CameraAdapterException("INVALID_PROTOCOL_RESPONSE");
        return data;
    }

    public static void requireCode(String code) {
        if (code == null
                || code.isBlank()
                || code.length() > 512
                || code.chars().anyMatch(Character::isISOControl))
            throw new CameraAdapterException("INVALID_CHANNEL_IDENTITY");
    }

    private static void requirePage(int page, int size) {
        if (page < 1 || page > 100000 || size < 1 || size > 100)
            throw new CameraAdapterException("DISCOVERY_LIMIT_EXCEEDED");
    }
}
