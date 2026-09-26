package com.streamfusion.platform.audit.pojo.dto;

import java.time.LocalDateTime;

public record AuditCriteria(
        String userPattern,
        Long actorId,
        String action,
        String module,
        String result,
        String traceId,
        LocalDateTime startTime,
        LocalDateTime endTime) {}
