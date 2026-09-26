package com.streamfusion.platform.audit.service.impl;

import com.baomidou.mybatisplus.spring.service.impl.ServiceImpl;
import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.streamfusion.platform.audit.mapper.OperationLogMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.pojo.entity.OperationLogEntity;
import com.streamfusion.platform.audit.service.AuditService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.slf4j.MDC;
import org.springframework.stereotype.Service;

@Service
public class AuditServiceImpl extends ServiceImpl<OperationLogMapper, OperationLogEntity>
        implements AuditService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");
    private static final int MAX_IDS = 1000;
    private static final int MAX_SUMMARY_LENGTH = 128;
    private static final Set<String> OBJECT_FIELDS =
            Set.of("name", "code", "username", "nickname", "parentId");
    private static final Set<String> RELATION_FIELDS =
            Set.of(
                    "roleIds",
                    "menuIds",
                    "departmentIds",
                    "beforeRoleIds",
                    "afterRoleIds",
                    "beforeMenuIds",
                    "afterMenuIds",
                    "beforeDepartmentIds",
                    "afterDepartmentIds");
    private final ObjectMapper json;
    private final Clock clock;

    public AuditServiceImpl(OperationLogMapper mapper, ObjectMapper json, Clock clock) {
        this.baseMapper = mapper;
        this.json = json;
        this.clock = clock;
    }

    @Override
    public boolean hasSuccessfulBootstrap() {
        return baseMapper.countSuccessfulBootstrap() > 0;
    }

    @Override
    public void record(
            Long actorId,
            String targetType,
            Long targetId,
            String action,
            String result,
            String reasonCode,
            AuditContextDto context) {
        record(actorId, targetType, targetId, action, result, reasonCode, context, Map.of());
    }

    @Override
    public void record(
            Long actorId,
            String targetType,
            Long targetId,
            String action,
            String result,
            String reasonCode,
            AuditContextDto context,
            Map<String, ?> changes) {
        OperationLogEntity entry = new OperationLogEntity();
        entry.setActorId(actorId);
        entry.setTargetType(targetType);
        entry.setTargetId(targetId);
        entry.setAction(action);
        entry.setResult(result);
        entry.setReasonCode(reasonCode);
        entry.setChanges(serializeChanges(changes));
        entry.setTraceId(MDC.get("traceId"));
        entry.setCreatedAt(LocalDateTime.now(clock.withZone(BUSINESS_ZONE)));
        if (context != null) {
            entry.setSourceIp(context.getSourceIp());
            entry.setClientSummary(context.getClientSummary());
        }
        if (!save(entry)) throw new IllegalStateException("Audit record was not persisted");
    }

    private String serializeChanges(Map<String, ?> changes) {
        if (changes == null || changes.isEmpty()) return null;
        Map<String, Object> selected = new LinkedHashMap<>();
        changes.forEach(
                (key, value) -> {
                    if (OBJECT_FIELDS.contains(key)) {
                        if (value == null) selected.put(key, null);
                        else if (value instanceof String text
                                && text.codePointCount(0, text.length()) <= MAX_SUMMARY_LENGTH) {
                            selected.put(key, text);
                        } else throw invalidSummary();
                    } else if (RELATION_FIELDS.contains(key)
                            && value instanceof List<?> ids
                            && ids.size() <= MAX_IDS) {
                        selected.put(key, ids.stream().map(AuditServiceImpl::auditId).toList());
                    } else throw invalidSummary();
                });
        try {
            return json.writeValueAsString(selected);
        } catch (JsonProcessingException ex) {
            throw new IllegalStateException("Audit summary cannot be serialized", ex);
        }
    }

    private static String auditId(Object value) {
        if (value instanceof Long id && id > 0) return id.toString();
        if (value instanceof String text && text.matches("[1-9][0-9]{0,18}")) {
            try {
                if (Long.parseLong(text) > 0) return text;
            } catch (NumberFormatException ignored) {
                // An ID outside BIGINT cannot describe a persisted relation.
            }
        }
        throw invalidSummary();
    }

    private static IllegalArgumentException invalidSummary() {
        return new IllegalArgumentException("Audit summary contains an unsupported field or value");
    }
}
