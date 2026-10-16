package com.streamfusion.platform.monitor.audit.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.monitor.audit.pojo.entity.OperationLogEntity;
import org.apache.ibatis.annotations.Mapper;

@Mapper
public interface OperationLogMapper extends BaseMapper<OperationLogEntity> {
    long countSuccessfulBootstrap();
}
