package com.streamfusion.platform.audit.service;

import com.streamfusion.platform.audit.mapper.AuditReferenceMapper;
import com.streamfusion.platform.audit.pojo.vo.AuditReferenceVo;
import java.util.ArrayList;
import java.util.Collection;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Component;

/**
 * Batch-only narrow projections: no dependency on mutable domain services or entity serializers.
 */
@Component
@RequiredArgsConstructor
public class AuditReferences {
    private static final Set<String> TYPES = Set.of("USER", "ROLE", "DEPT", "MENU", "IP_BLOCK");
    private final AuditReferenceMapper mapper;

    public Map<String, AuditReferenceVo> current(Collection<Request> requests) {
        Map<String, Set<Long>> grouped = new LinkedHashMap<>();
        for (Request request : requests) {
            if (request.id() != null && TYPES.contains(request.type()))
                grouped.computeIfAbsent(request.type(), ignored -> new LinkedHashSet<>())
                        .add(Long.parseLong(request.id()));
        }
        Map<String, AuditReferenceVo> result = new LinkedHashMap<>();
        grouped.forEach(
                (type, values) -> {
                    List<Long> ids = new ArrayList<>(values);
                    for (int start = 0; start < ids.size(); start += 1000) {
                        for (var subject :
                                mapper.find(
                                        type,
                                        ids.subList(start, Math.min(start + 1000, ids.size())))) {
                            String name = safeText(subject.getName());
                            String code = safeText(subject.getCode());
                            result.put(
                                    AuditChanges.key(type, subject.getId().toString()),
                                    new AuditReferenceVo(
                                            subject.getId().toString(),
                                            type,
                                            name,
                                            code,
                                            "CURRENT"));
                        }
                    }
                });
        return result;
    }

    public AuditReferenceVo resolve(
            String type,
            String id,
            AuditReferenceVo snapshot,
            Map<String, AuditReferenceVo> current) {
        if (id == null) return null;
        if (snapshot != null && id.equals(snapshot.id()) && type.equals(snapshot.type()))
            return snapshot;
        return current.getOrDefault(
                AuditChanges.key(type, id), new AuditReferenceVo(id, type, null, null, "MISSING"));
    }

    public String capture(
            Long actorId, String type, Long targetId, Map<String, ?> values, AuditChanges changes) {
        Map<String, Object> fields = changes.select(values);
        List<Request> wanted = new ArrayList<>();
        wanted.add(new Request("USER", string(actorId)));
        wanted.add(new Request(type, string(targetId)));
        var relationIds = changes.relationIds(fields, type);
        relationIds.forEach(
                (field, ids) ->
                        ids.forEach(
                                id ->
                                        wanted.add(
                                                new Request(
                                                        changes.relationType(field, type), id))));
        var known = current(wanted);
        var actor = known.get(AuditChanges.key("USER", string(actorId)));
        var target = known.get(AuditChanges.key(type, string(targetId)));
        // Deleted targets are absent from current tables; callers already captured these safe
        // fields.
        if (target == null && targetId != null) {
            String name = (String) fields.get(type.equals("USER") ? "nickname" : "name");
            String code = (String) fields.get(type.equals("USER") ? "username" : "code");
            if (name == null) name = code;
            if (name != null || code != null)
                target = new AuditReferenceVo(targetId.toString(), type, name, code, "SNAPSHOT");
        }
        Map<String, AuditReferenceVo> snapshots = new LinkedHashMap<>();
        relationIds.forEach(
                (field, ids) ->
                        ids.forEach(
                                id -> {
                                    String key =
                                            AuditChanges.key(changes.relationType(field, type), id);
                                    AuditReferenceVo reference = known.get(key);
                                    if (reference != null) snapshots.put(key, reference);
                                }));
        return changes.write(fields, actor, target, snapshots);
    }

    private static String string(Long value) {
        return value == null ? null : value.toString();
    }

    private static String safeText(String value) {
        if (value == null) return null;
        return value.codePoints()
                .filter(cp -> !Character.isISOControl(cp))
                .limit(128)
                .collect(StringBuilder::new, StringBuilder::appendCodePoint, StringBuilder::append)
                .toString();
    }

    public record Request(String type, String id) {}
}
