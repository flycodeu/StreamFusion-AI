package com.streamfusion.platform.audit.service;

import com.streamfusion.platform.audit.mapper.AuditReadMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditCriteria;
import com.streamfusion.platform.audit.pojo.dto.AuditQueryDto;
import com.streamfusion.platform.audit.pojo.dto.AuditRow;
import com.streamfusion.platform.audit.pojo.vo.AuditDetailVo;
import com.streamfusion.platform.audit.pojo.vo.AuditEntryVo;
import com.streamfusion.platform.audit.pojo.vo.AuditReferenceVo;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class AuditQueryService {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final AuditReadMapper mapper;
    private final AuditChanges changes;
    private final AuditReferences references;

    public PageResultVo<AuditEntryVo> page(AuditQueryDto query) {
        if (query == null) query = new AuditQueryDto();
        var pagination = query.<AuditRow>toPage();
        String user = text(query.getUser(), 64, "user");
        Long actor = null;
        if (user != null && user.matches("[1-9][0-9]{0,18}")) {
            try {
                actor = Long.parseLong(user);
            } catch (NumberFormatException ignored) {
            }
        }
        String result = code(query.getResult(), "result");
        if (result != null && !Set.of("SUCCESS", "FAILURE", "DENIED").contains(result))
            throw invalid("result");
        String traceId = text(query.getTraceId(), 32, "traceId");
        if (traceId != null) {
            traceId = traceId.toLowerCase(Locale.ROOT);
            if (!traceId.matches("[a-f0-9]{32}")) throw invalid("traceId");
        }
        LocalDateTime start = time(query.getStartTime(), "startTime");
        LocalDateTime end = time(query.getEndTime(), "endTime");
        if (start != null && end != null && start.isAfter(end)) throw invalid("endTime");
        String pattern =
                user == null
                        ? null
                        : "%" + user.replace("!", "!!").replace("%", "!%").replace("_", "!_") + "%";
        var resultPage =
                mapper.page(
                        pagination,
                        new AuditCriteria(
                                pattern,
                                actor,
                                code(query.getAction(), "action"),
                                code(query.getModule(), "module"),
                                result,
                                traceId,
                                start,
                                end));
        List<AuditReferences.Request> wanted = new ArrayList<>();
        resultPage.getRecords().forEach(row -> addSubjects(wanted, row));
        var current = references.current(wanted);
        return PageResultVo.from(
                resultPage,
                resultPage.getRecords().stream()
                        .map(row -> entry(row, changes.read(row.getChanges()), current))
                        .toList());
    }

    public AuditDetailVo detail(String id) {
        AuditRow row = mapper.detail(DecimalInput.id(id, "id"));
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        var parsed = changes.read(row.getChanges());
        var relationIds = changes.relationIds(parsed.fields(), row.getTargetType());
        List<AuditReferences.Request> wanted = new ArrayList<>();
        addSubjects(wanted, row);
        relationIds.forEach(
                (field, ids) ->
                        ids.forEach(
                                value ->
                                        wanted.add(
                                                new AuditReferences.Request(
                                                        changes.relationType(
                                                                field, row.getTargetType()),
                                                        value))));
        var current = references.current(wanted);
        Map<String, List<AuditReferenceVo>> relations = new LinkedHashMap<>();
        relationIds.forEach(
                (field, ids) -> {
                    String type = changes.relationType(field, row.getTargetType());
                    relations.put(
                            field,
                            ids.stream()
                                    .map(
                                            value ->
                                                    references.resolve(
                                                            type,
                                                            value,
                                                            parsed.references()
                                                                    .get(
                                                                            AuditChanges.key(
                                                                                    type, value)),
                                                            current))
                                    .toList());
                });
        return new AuditDetailVo(
                entry(row, parsed, current),
                row.getSourceIp(),
                parsed.fields(),
                Collections.unmodifiableMap(relations));
    }

    private AuditEntryVo entry(
            AuditRow row, AuditChanges.Parsed parsed, Map<String, AuditReferenceVo> current) {
        return new AuditEntryVo(
                String.valueOf(row.getId()),
                asId(row.getActorId()),
                row.getUsername(),
                row.getNickname(),
                row.getTargetType(),
                asId(row.getTargetId()),
                row.getAction(),
                row.getResult(),
                row.getReasonCode(),
                row.getTraceId(),
                row.getCreatedAt().atZone(ZONE).toInstant(),
                references.resolve(
                        "USER",
                        asId(row.getActorId()),
                        parsed == null ? null : parsed.actor(),
                        current),
                references.resolve(
                        row.getTargetType(),
                        asId(row.getTargetId()),
                        parsed == null ? null : parsed.target(),
                        current));
    }

    private static void addSubjects(List<AuditReferences.Request> wanted, AuditRow row) {
        wanted.add(new AuditReferences.Request("USER", asId(row.getActorId())));
        wanted.add(new AuditReferences.Request(row.getTargetType(), asId(row.getTargetId())));
    }

    private static String asId(Long value) {
        return value == null ? null : value.toString();
    }

    private static String code(String value, String field) {
        String result = text(value, 64, field);
        if (result != null && !result.matches("[A-Z][A-Z0-9_]*")) throw invalid(field);
        return result;
    }

    private static String text(String value, int max, String field) {
        if (value == null || value.isBlank()) return null;
        String result = value.strip();
        if (result.length() > max) throw invalid(field);
        return result;
    }

    private static LocalDateTime time(String value, String field) {
        if (value == null || value.isBlank()) return null;
        try {
            return LocalDateTime.ofInstant(Instant.parse(value), ZONE);
        } catch (RuntimeException ignored) {
            throw invalid(field);
        }
    }

    private static BusinessException invalid(String field) {
        return BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(List.of(ValidationDetails.FieldError.invalid(field))));
    }
}
