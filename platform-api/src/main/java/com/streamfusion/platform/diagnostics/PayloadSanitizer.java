package com.streamfusion.platform.diagnostics;

import com.fasterxml.jackson.databind.*;
import com.fasterxml.jackson.databind.node.*;
import com.streamfusion.platform.common.logging.LogRedactor;
import java.beans.Introspector;
import java.lang.reflect.Method;
import java.nio.charset.StandardCharsets;
import java.util.*;
import org.springframework.stereotype.Component;

/** Project selected fields before serialization; never serialize an entire request/response. */
@Component
public class PayloadSanitizer {
    private static final Set<String> FIELDS =
            Set.of(
                    "query",
                    "id",
                    "userId",
                    "actorId",
                    "cameraId",
                    "streamProfileId",
                    "profileId",
                    "deviceId",
                    "sourceId",
                    "groupId",
                    "parentId",
                    "planId",
                    "checkId",
                    "groupKey",
                    "groupKeys",
                    "version",
                    "name",
                    "label",
                    "username",
                    "nickname",
                    "code",
                    "msg",
                    "message",
                    "status",
                    "enabled",
                    "lifecycle",
                    "mode",
                    "format",
                    "scope",
                    "intervalMinutes",
                    "durationHours",
                    "clientRequestId",
                    "defaultPreviewProfileId",
                    "usageHint",
                    "brand",
                    "provider",
                    "protocol",
                    "page",
                    "size",
                    "total",
                    "items",
                    "data",
                    "success",
                    "result",
                    "reasonCode",
                    "state",
                    "executionState",
                    "probeKind",
                    "freshness",
                    "layer",
                    "configurationCurrent",
                    "canCheck",
                    "blockingReasonCode",
                    "observation",
                    "observedAt",
                    "requestedAt",
                    "finishedAt",
                    "validUntil",
                    "serverNow",
                    "timestamp",
                    "traceId",
                    "targetCount",
                    "blockedCount",
                    "progress",
                    "failed",
                    "pending",
                    "skipped",
                    "unknown",
                    "fields",
                    "field",
                    "error",
                    "errors",
                    "profiles",
                    "targets",
                    "limitExceeded",
                    "roleIds",
                    "menuIds",
                    "departmentIds",
                    "sortOrder",
                    "visible",
                    "type",
                    "path",
                    "routeName",
                    "componentKey",
                    "moduleKey",
                    "modules",
                    "startTime",
                    "endTime");
    private final ObjectMapper json;

    public PayloadSanitizer(ObjectMapper json) {
        this.json = json;
    }

    public JsonNode project(Object value) {
        try {
            var result = select(value, 0, new int[] {256});
            if (json.writeValueAsBytes(result).length <= 8192) return result;
        } catch (Exception ignored) {
            return JsonNodeFactory.instance.objectNode().put("captureNotice", "内容不可安全读取，已省略");
        }
        return JsonNodeFactory.instance.objectNode().put("captureNotice", "内容超过8 KiB，已省略");
    }

    public JsonNode parse(byte[] bytes) throws java.io.IOException {
        return json.readTree(bytes);
    }

    private JsonNode select(Object value, int depth, int[] budget) {
        var factory = JsonNodeFactory.instance;
        if (value == null) return NullNode.instance;
        if (depth > 6 || --budget[0] < 0) return TextNode.valueOf("[超过采集上限]");
        if (value instanceof JsonNode node) {
            if (node.isTextual()) return text(node.textValue());
            if (node.isNumber() || node.isBoolean() || node.isNull()) return node.deepCopy();
            if (node.isArray()) {
                var out = factory.arrayNode();
                for (int i = 0; i < Math.min(20, node.size()); i++)
                    out.add(select(node.get(i), depth + 1, budget));
                if (node.size() > 20) out.add("[其余项已省略]");
                return out;
            }
            var out = factory.objectNode();
            node.fields()
                    .forEachRemaining(
                            entry -> {
                                if (FIELDS.contains(entry.getKey()) && budget[0] > 0)
                                    out.set(
                                            entry.getKey(),
                                            select(entry.getValue(), depth + 1, budget));
                            });
            return out;
        }
        if (value instanceof CharSequence) return text(value.toString());
        if (value instanceof Boolean b) return BooleanNode.valueOf(b);
        if (value instanceof Byte
                || value instanceof Short
                || value instanceof Integer
                || value instanceof Long) return LongNode.valueOf(((Number) value).longValue());
        if (value instanceof Number n) {
            try {
                return DecimalNode.valueOf(new java.math.BigDecimal(n.toString()));
            } catch (NumberFormatException ex) {
                return TextNode.valueOf("[无效数值]");
            }
        }
        if (value instanceof Enum<?> e) return TextNode.valueOf(e.name());
        if (value instanceof java.time.temporal.TemporalAccessor)
            return TextNode.valueOf(value.toString());
        if (value instanceof Collection<?> list) {
            var out = factory.arrayNode();
            int n = 0;
            for (var item : list) {
                if (n++ == 20) {
                    out.add("[其余项已省略]");
                    break;
                }
                out.add(select(item, depth + 1, budget));
            }
            return out;
        }
        if (value instanceof Map<?, ?> map) {
            var out = factory.objectNode();
            for (var entry : map.entrySet())
                if (entry.getKey() instanceof String key && FIELDS.contains(key) && budget[0] > 0)
                    out.set(key, select(entry.getValue(), depth + 1, budget));
            return out;
        }
        if (!value.getClass().getPackageName().startsWith("com.streamfusion.platform"))
            return TextNode.valueOf("[类型未适配]");
        var out = factory.objectNode();
        try {
            if (value.getClass().isRecord()) {
                for (var field : value.getClass().getRecordComponents())
                    if (FIELDS.contains(field.getName()) && budget[0] > 0)
                        out.set(
                                field.getName(),
                                select(field.getAccessor().invoke(value), depth + 1, budget));
            } else
                for (var property :
                        Introspector.getBeanInfo(value.getClass(), Object.class)
                                .getPropertyDescriptors()) {
                    Method read = property.getReadMethod();
                    if (read != null && FIELDS.contains(property.getName()) && budget[0] > 0)
                        out.set(property.getName(), select(read.invoke(value), depth + 1, budget));
                }
        } catch (Exception ignored) {
            out.put("captureNotice", "部分字段不可读取");
        }
        return out;
    }

    private static TextNode text(String value) {
        if (value.length() > 2048) return TextNode.valueOf("[字段超长，已省略]");
        if (value.matches("(?is).*\\b(?:rtsp|rtsps|https?)://.*"))
            return TextNode.valueOf("[地址已隐藏]");
        String safe = LogRedactor.redact(value);
        if (safe.getBytes(StandardCharsets.UTF_8).length > 512)
            safe = safe.substring(0, Math.min(128, safe.length())) + "[已省略]";
        return TextNode.valueOf(safe);
    }
}
