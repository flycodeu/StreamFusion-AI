package com.streamfusion.platform.role.service;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.role.pojo.dto.RoleCreateDto;
import com.streamfusion.platform.role.pojo.dto.RoleMenusUpdateDto;
import com.streamfusion.platform.role.pojo.dto.RoleQueryDto;
import com.streamfusion.platform.role.pojo.dto.RoleUpdateDto;
import com.streamfusion.platform.role.pojo.vo.RoleDetailVo;
import com.streamfusion.platform.role.pojo.vo.RoleMenusVo;
import com.streamfusion.platform.role.pojo.vo.RoleOptionVo;
import java.util.List;

public interface RoleService {
    PageResultVo<RoleDetailVo> page(RoleQueryDto query);

    List<RoleOptionVo> options();

    RoleDetailVo create(RoleCreateDto input, AuditContextDto context);

    RoleDetailVo update(String id, RoleUpdateDto input, AuditContextDto context);

    RoleDetailVo changeStatus(String id, String version, boolean enabled, AuditContextDto context);

    void delete(String id, String version, AuditContextDto context);

    RoleMenusVo menus(String id);

    RoleMenusVo updateMenus(String id, RoleMenusUpdateDto input, AuditContextDto context);
}
