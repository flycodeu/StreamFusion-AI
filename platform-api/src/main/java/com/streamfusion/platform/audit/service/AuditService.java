package com.streamfusion.platform.audit.service;

import com.baomidou.mybatisplus.spring.service.IService;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.pojo.entity.OperationLogEntity;
import java.util.Map;

public interface AuditService extends IService<OperationLogEntity> {
    boolean hasSuccessfulBootstrap();

    /** Transaction ownership stays with the calling operation, including rollback on failure. */
    void record(
            Long actorId,
            String targetType,
            Long targetId,
            String action,
            String result,
            String reasonCode,
            AuditContextDto context);

    /**
     * Records only explicitly selected object and relationship fields, never a request or entity.
     */
    void record(
            Long actorId,
            String targetType,
            Long targetId,
            String action,
            String result,
            String reasonCode,
            AuditContextDto context,
            Map<String, ?> changes);
}
