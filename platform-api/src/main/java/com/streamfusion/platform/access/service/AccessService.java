package com.streamfusion.platform.access.service;

import com.baomidou.mybatisplus.spring.service.IService;
import com.streamfusion.platform.access.pojo.dto.AccessSnapshotDto;
import com.streamfusion.platform.role.pojo.entity.RoleEntity;
import java.time.LocalDateTime;

public interface AccessService extends IService<RoleEntity> {
    AccessSnapshotDto snapshot(long userId);

    /** 按用户、有效角色和模块注册关系检查能力；模块内接口不再逐项配置权限码。 */
    boolean hasModuleAccess(long userId, String module);

    boolean isSuperAdmin(long userId);

    /** 为内置超级管理员补齐现有页面关联，重复调用不新增重复关系。 */
    void bindAllPages(long roleId, LocalDateTime now);
}
