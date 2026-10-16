package com.streamfusion.platform.monitor.audit.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.monitor.audit.maintenance.AuditExportEntity;
import java.time.LocalDateTime;
import org.apache.ibatis.annotations.*;

@Mapper
public interface AuditExportMapper extends BaseMapper<AuditExportEntity> {
    @Select(
            "SELECT COUNT(*) FROM sys_audit_export WHERE (state='QUEUED' AND expires_at>#{now}) OR (state='RUNNING' AND lease_until>#{now})")
    long activeCount(LocalDateTime now);

    @Select("SELECT COUNT(*) FROM sys_audit_export WHERE file_removed=FALSE")
    long liveFiles();

    @Select(
            "SELECT * FROM sys_audit_export WHERE storage_key=#{storage} AND state='QUEUED' AND expires_at>#{now} ORDER BY created_at,id LIMIT 1 FOR UPDATE")
    AuditExportEntity next(@Param("storage") String storage, @Param("now") LocalDateTime now);

    @Update(
            "UPDATE sys_audit_export SET state='FAILED',reason_code='WORKER_LOST',finished_at=#{now} WHERE state='RUNNING' AND lease_until<#{now}")
    int expireLeases(LocalDateTime now);
}
