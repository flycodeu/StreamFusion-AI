package com.streamfusion.platform.auth.guard.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.auth.guard.pojo.entity.IpBlockEntity;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface IpBlockMapper extends BaseMapper<IpBlockEntity> {
    IpBlockEntity find(@Param("ip") String ip);

    IpBlockEntity lock(@Param("ip") String ip);

    void ensure(@Param("row") IpBlockEntity row);
}
