package com.streamfusion.platform.loginrecord.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.loginrecord.pojo.entity.LoginRecordEntity;
import java.time.LocalDateTime;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface LoginRecordMapper extends BaseMapper<LoginRecordEntity> {
    int touch(
            @Param("id") long id,
            @Param("now") LocalDateTime now,
            @Param("before") LocalDateTime before);

    int end(
            @Param("id") long id,
            @Param("reason") String reason,
            @Param("now") LocalDateTime now,
            @Param("active") boolean active);

    int endAll(
            @Param("userId") long userId,
            @Param("reason") String reason,
            @Param("now") LocalDateTime now);
}
