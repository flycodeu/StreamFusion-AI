package com.streamfusion.platform.department.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.department.pojo.entity.DepartmentEntity;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface DepartmentMapper extends BaseMapper<DepartmentEntity> {
    List<DepartmentMemberCount> memberCounts();

    List<UserDepartmentAssignment> userDepartments(@Param("userIds") List<Long> userIds);

    long userCount(@Param("deptId") long deptId);

    int deleteUserBindings(@Param("userId") long userId);

    int insertUserBinding(
            @Param("userId") long userId,
            @Param("deptId") long deptId,
            @Param("now") LocalDateTime now,
            @Param("actorId") long actorId);
}
