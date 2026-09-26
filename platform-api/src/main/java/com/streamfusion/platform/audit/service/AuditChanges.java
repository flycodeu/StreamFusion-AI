package com.streamfusion.platform.audit.service;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.audit.pojo.vo.AuditReferenceVo;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/** One allowlist for writes and historical reads; metadata never escapes through raw changes. */
@Component
@RequiredArgsConstructor
public class AuditChanges {
    static final int MAX_JSON_LENGTH = 1_048_576;
    static final int MAX_IDS = 1000;
    private static final Set<String> TEXT =
            Set.of("name", "code", "username", "nickname", "parentId");
    private static final Map<String, String> RELATIONS =
            Map.of(
                    "roleIds",
                    "ROLE",
                    "beforeRoleIds",
                    "ROLE",
                    "afterRoleIds",
                    "ROLE",
                    "menuIds",
                    "MENU",
                    "beforeMenuIds",
                    "MENU",
                    "afterMenuIds",
                    "MENU",
                    "departmentIds",
                    "DEPT",
                    "beforeDepartmentIds",
                    "DEPT",
                    "afterDepartmentIds",
                    "DEPT");
    private final ObjectMapper json;

    public Map<String, Object> select(Map<String, ?> input) {
        if (input == null || input.isEmpty()) return Map.of();
        Map<String, Object> result = new LinkedHashMap<>();
        input.forEach(
                (key, value) -> {
                    if (TEXT.contains(key)) {
                        if (value == null) result.put(key, null);
                        else if (value instanceof String text && bounded(text))
                            result.put(key, text);
                        else throw invalid();
                    } else if (RELATIONS.containsKey(key)
                            && value instanceof List<?> values
                            && values.size() <= MAX_IDS) {
                        result.put(key, values.stream().map(AuditChanges::id).toList());
                    } else throw invalid();
                });
        return Collections.unmodifiableMap(result);
    }

    public Parsed read(String value) {
        if (value == null || value.length() > MAX_JSON_LENGTH) return Parsed.empty();
        try {
            JsonNode root = json.readTree(value);
            if (!root.isObject()) return Parsed.empty();
            Map<String, Object> fields = new LinkedHashMap<>();
            root.properties()
                    .forEach(
                            field -> {
                                String key = field.getKey();
                                JsonNode item = field.getValue();
                                if (TEXT.contains(key)) {
                                    if (item.isNull()) fields.put(key, null);
                                    else if (item.isTextual() && bounded(item.asText()))
                                        fields.put(key, item.asText());
                                } else if (RELATIONS.containsKey(key)
                                        && item.isArray()
                                        && item.size() <= MAX_IDS) {
                                    List<String> ids = new ArrayList<>();
                                    for (JsonNode candidate : item) {
                                        if (!candidate.isTextual()) return;
                                        try {
                                            ids.add(id(candidate.asText()));
                                        } catch (IllegalArgumentException ignored) {
                                            return;
                                        }
                                    }
                                    fields.put(key, List.copyOf(ids));
                                }
                            });
            Map<String, AuditReferenceVo> references = new LinkedHashMap<>();
            JsonNode snapshots = root.path("_relations");
            // At most nine bounded relation arrays plus actor/target; ignore arbitrary nested JSON.
            if (snapshots.isObject() && snapshots.size() <= RELATIONS.size() * MAX_IDS + 1) {
                snapshots
                        .properties()
                        .forEach(
                                field -> {
                                    AuditReferenceVo reference = snapshot(field.getValue());
                                    if (reference != null
                                            && field.getKey()
                                                    .equals(key(reference.type(), reference.id())))
                                        references.put(field.getKey(), reference);
                                });
            }
            return new Parsed(
                    Collections.unmodifiableMap(fields),
                    Map.copyOf(references),
                    snapshot(root.path("_actor")),
                    snapshot(root.path("_target")));
        } catch (Exception ignored) {
            return Parsed.empty();
        }
    }

    public String write(
            Map<String, Object> fields,
            AuditReferenceVo actor,
            AuditReferenceVo target,
            Map<String, AuditReferenceVo> relations) {
        Map<String, Object> result = new LinkedHashMap<>();
        if (actor != null) result.put("_actor", stored(actor));
        if (target != null) result.put("_target", stored(target));
        result.putAll(fields);
        if (!relations.isEmpty()) {
            Map<String, Object> unique = new LinkedHashMap<>();
            relations.forEach((key, reference) -> unique.put(key, stored(reference)));
            result.put("_relations", unique);
        }
        if (result.isEmpty()) return null;
        try {
            String text = json.writeValueAsString(result);
            if (text.length() > MAX_JSON_LENGTH) throw invalid();
            return text;
        } catch (com.fasterxml.jackson.core.JsonProcessingException ex) {
            throw new IllegalStateException("Audit summary cannot be serialized", ex);
        }
    }

    public Map<String, List<String>> relationIds(Map<String, Object> fields, String targetType) {
        Map<String, List<String>> result = new LinkedHashMap<>();
        fields.forEach(
                (field, value) -> {
                    if (RELATIONS.containsKey(field) && value instanceof List<?> values)
                        result.put(field, values.stream().map(AuditChanges::id).toList());
                    else if (field.equals("parentId")
                            && Set.of("DEPT", "MENU").contains(targetType)) {
                        if (value == null) result.put(field, List.of());
                        else {
                            try {
                                result.put(field, List.of(id(value)));
                            } catch (IllegalArgumentException ignored) {
                            }
                        }
                    }
                });
        return result;
    }

    public String relationType(String field, String targetType) {
        return field.equals("parentId") ? targetType : RELATIONS.get(field);
    }

    public static String key(String type, String id) {
        return type + ":" + id;
    }

    static boolean bounded(String value) {
        return value.codePointCount(0, value.length()) <= 128;
    }

    private static String id(Object value) {
        if (value instanceof Long number && number > 0) return number.toString();
        if (value instanceof String text && text.matches("[1-9][0-9]{0,18}")) {
            try {
                if (Long.parseLong(text) > 0) return text;
            } catch (NumberFormatException ignored) {
            }
        }
        throw invalid();
    }

    private static AuditReferenceVo snapshot(JsonNode value) {
        if (!value.isObject() || value.size() != 4) return null;
        try {
            String identifier = id(value.path("id").isTextual() ? value.path("id").asText() : null);
            JsonNode type = value.path("type"),
                    name = value.path("name"),
                    code = value.path("code");
            if (!type.isTextual()
                    || !type.asText().matches("[A-Z][A-Z0-9_]{0,63}")
                    || !(name.isNull() || name.isTextual() && bounded(name.asText()))
                    || !(code.isNull() || code.isTextual() && bounded(code.asText()))) return null;
            return new AuditReferenceVo(
                    identifier,
                    type.asText(),
                    name.isNull() ? null : name.asText(),
                    code.isNull() ? null : code.asText(),
                    "SNAPSHOT");
        } catch (IllegalArgumentException ignored) {
            return null;
        }
    }

    private static Map<String, Object> stored(AuditReferenceVo value) {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("id", id(value.id()));
        if (value.type() == null
                || !value.type().matches("[A-Z][A-Z0-9_]{0,63}")
                || value.name() != null && !bounded(value.name())
                || value.code() != null && !bounded(value.code())) throw invalid();
        result.put("type", value.type());
        result.put("name", value.name());
        result.put("code", value.code());
        return result;
    }

    private static IllegalArgumentException invalid() {
        return new IllegalArgumentException("Audit summary contains an unsupported field or value");
    }

    public record Parsed(
            Map<String, Object> fields,
            Map<String, AuditReferenceVo> references,
            AuditReferenceVo actor,
            AuditReferenceVo target) {
        static Parsed empty() {
            return new Parsed(Map.of(), Map.of(), null, null);
        }
    }
}
