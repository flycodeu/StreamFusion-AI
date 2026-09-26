package com.streamfusion.platform.role.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.role.pojo.entity.RoleEntity;
import java.time.LocalDateTime;
import java.util.List;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface RoleMapper extends BaseMapper<RoleEntity> {
    List<Long> menuIds(@Param("roleId") long roleId);

    int deleteMenus(@Param("roleId") long roleId);

    int insertMenu(
            @Param("roleId") long roleId,
            @Param("menuId") long menuId,
            @Param("now") LocalDateTime now,
            @Param("actorId") long actorId);

    long userCount(@Param("roleId") long roleId);

    List<Long> userRoleIds(@Param("userId") long userId);

    List<RoleEntity> assignedRoles(@Param("userId") long userId);

    int deleteUserRoles(@Param("userId") long userId);

    int insertUserRole(
            @Param("userId") long userId,
            @Param("roleId") long roleId,
            @Param("now") LocalDateTime now,
            @Param("actorId") long actorId);
}
