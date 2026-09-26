package com.streamfusion.platform.audit.mapper;

import com.baomidou.mybatisplus.core.metadata.IPage;
import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.streamfusion.platform.audit.pojo.dto.AuditCriteria;
import com.streamfusion.platform.audit.pojo.dto.AuditRow;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface AuditReadMapper {
    IPage<AuditRow> page(Page<AuditRow> page, @Param("q") AuditCriteria criteria);

    AuditRow detail(@Param("id") long id);
}
