package com.streamfusion.platform.access.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.access.pojo.dto.UserRoleAssignmentDto;
import com.streamfusion.platform.access.pojo.vo.RoleVo;
import com.streamfusion.platform.role.pojo.entity.RoleEntity;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface AccessMapper extends BaseMapper<RoleEntity> {
    Long lockSuperAdminRole();

    List<RoleVo> findRoles(@Param("userId") long userId);

    List<UserRoleAssignmentDto> findRolesForUsers(@Param("userIds") List<Long> userIds);

    List<String> findModules(@Param("userId") long userId);

    boolean hasModuleAccess(@Param("userId") long userId, @Param("module") String module);

    boolean isSuperAdmin(@Param("userId") long userId);

    int bindAllPages(@Param("roleId") long roleId, @Param("now") LocalDateTime now);

    int bindRole(
            @Param("userId") long userId,
            @Param("roleId") long roleId,
            @Param("now") LocalDateTime now);
}
