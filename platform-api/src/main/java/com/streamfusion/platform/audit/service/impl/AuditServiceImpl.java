package com.streamfusion.platform.audit.service.impl;

import com.baomidou.mybatisplus.spring.service.impl.ServiceImpl;
import com.streamfusion.platform.audit.mapper.OperationLogMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.pojo.entity.OperationLogEntity;
import com.streamfusion.platform.audit.service.AuditChanges;
import com.streamfusion.platform.audit.service.AuditReferences;
import com.streamfusion.platform.audit.service.AuditService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.Map;
import org.slf4j.MDC;
import org.springframework.stereotype.Service;

@Service
public class AuditServiceImpl extends ServiceImpl<OperationLogMapper, OperationLogEntity>
        implements AuditService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");
    private final Clock clock;
    private final AuditChanges changes;
    private final AuditReferences references;

    public AuditServiceImpl(
            OperationLogMapper mapper,
            Clock clock,
            AuditChanges changes,
            AuditReferences references) {
        this.baseMapper = mapper;
        this.clock = clock;
        this.changes = changes;
        this.references = references;
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
        entry.setChanges(references.capture(actorId, targetType, targetId, changes, this.changes));
        entry.setTraceId(MDC.get("traceId"));
        entry.setCreatedAt(LocalDateTime.now(clock.withZone(BUSINESS_ZONE)));
        if (context != null) {
            entry.setSourceIp(context.getSourceIp());
            entry.setClientSummary(context.getClientSummary());
        }
        if (!save(entry)) throw new IllegalStateException("Audit record was not persisted");
    }
}
