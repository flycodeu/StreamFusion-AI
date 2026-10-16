package com.streamfusion.platform.monitor.audit.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.monitor.audit.maintenance.AuditBoundary;
import com.streamfusion.platform.monitor.audit.maintenance.AuditRetentionEntity;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.*;

@Mapper
public interface AuditRetentionMapper extends BaseMapper<AuditRetentionEntity> {
    @Select("SELECT * FROM sys_audit_retention_policy WHERE id=1 FOR UPDATE")
    AuditRetentionEntity lock();

    @Select("SELECT COUNT(*) FROM sys_operation_log WHERE action='BOOTSTRAP' AND result='SUCCESS'")
    long protectedCount();

    @Select("SELECT COUNT(*) FROM sys_operation_log")
    long totalCount();

    @Select(
            "SELECT created_at,id FROM sys_operation_log WHERE NOT(action='BOOTSTRAP' AND result='SUCCESS') ORDER BY created_at DESC,id DESC LIMIT 1 OFFSET #{offset}")
    AuditBoundary boundary(@Param("offset") long offset);

    List<Long> candidates(
            @Param("cutoff") LocalDateTime cutoff,
            @Param("boundaryId") Long boundaryId,
            @Param("limit") int limit);

    int removeCandidates(@Param("ids") List<Long> ids);
}
