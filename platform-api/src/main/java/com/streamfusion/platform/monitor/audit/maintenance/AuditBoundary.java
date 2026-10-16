package com.streamfusion.platform.monitor.audit.maintenance;

import java.time.LocalDateTime;

public record AuditBoundary(LocalDateTime createdAt, Long id) {}
