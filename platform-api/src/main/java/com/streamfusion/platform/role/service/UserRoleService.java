package com.streamfusion.platform.role.service;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.menu.pojo.vo.MenuRouteVo;
import com.streamfusion.platform.role.pojo.dto.UserRolesUpdateDto;
import com.streamfusion.platform.role.pojo.vo.RoleOptionVo;
import com.streamfusion.platform.role.pojo.vo.UserRolesVo;
import java.util.List;

public interface UserRoleService {
    UserRolesVo get(String userId);

    List<RoleOptionVo> options();

    List<MenuRouteVo> routes(String userId);

    UserRolesVo update(String userId, UserRolesUpdateDto input, AuditContextDto context);
}
