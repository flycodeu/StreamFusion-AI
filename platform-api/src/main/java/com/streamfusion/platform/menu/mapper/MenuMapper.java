package com.streamfusion.platform.menu.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.streamfusion.platform.menu.pojo.entity.MenuEntity;
import org.apache.ibatis.annotations.Mapper;
import org.apache.ibatis.annotations.Param;

@Mapper
public interface MenuMapper extends BaseMapper<MenuEntity> {
    int bindRole(
            @Param("roleId") long roleId,
            @Param("menuId") long menuId,
            @Param("now") java.time.LocalDateTime now,
            @Param("actorId") long actorId);

    int deleteRoleBindings(@Param("menuId") long menuId);

    java.util.List<Long> authorizedPageIds(@Param("userId") long userId);
}
