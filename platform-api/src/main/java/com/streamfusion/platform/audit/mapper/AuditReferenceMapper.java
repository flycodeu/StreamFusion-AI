package com.streamfusion.platform.audit.mapper;

import com.streamfusion.platform.audit.pojo.dto.AuditSubject;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface AuditReferenceMapper {
    List<AuditSubject> find(@Param("type") String type, @Param("ids") List<Long> ids);
}
