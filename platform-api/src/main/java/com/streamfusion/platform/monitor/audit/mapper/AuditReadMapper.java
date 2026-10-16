package com.streamfusion.platform.monitor.audit.mapper;

import com.baomidou.mybatisplus.core.metadata.IPage;
import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditCriteria;
import com.streamfusion.platform.monitor.audit.pojo.dto.AuditRow;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface AuditReadMapper {
    List<AuditRow> exportBatch(
            @Param("q") AuditCriteria criteria,
            @Param("upperTime") java.time.LocalDateTime upperTime,
            @Param("upperId") Long upperId,
            @Param("afterTime") java.time.LocalDateTime afterTime,
            @Param("afterId") Long afterId);

    IPage<AuditRow> page(Page<AuditRow> page, @Param("q") AuditCriteria criteria);

    AuditRow detail(@Param("id") long id);

    List<AuditRow> latestIpReleases(@Param("ids") List<Long> ids);
}
