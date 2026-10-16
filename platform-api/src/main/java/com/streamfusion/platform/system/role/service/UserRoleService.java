package com.streamfusion.platform.system.role.service;

import com.streamfusion.platform.monitor.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.system.menu.pojo.vo.MenuRouteVo;
import com.streamfusion.platform.system.role.pojo.dto.UserRolesUpdateDto;
import com.streamfusion.platform.system.role.pojo.vo.RoleOptionVo;
import com.streamfusion.platform.system.role.pojo.vo.UserRolesVo;
import java.util.List;

public interface UserRoleService {
    UserRolesVo get(String userId);

    List<RoleOptionVo> options();

    List<MenuRouteVo> routes(String userId);

    UserRolesVo update(String userId, UserRolesUpdateDto input, AuditContextDto context);
}
