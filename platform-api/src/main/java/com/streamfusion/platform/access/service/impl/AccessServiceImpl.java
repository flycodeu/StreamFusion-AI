package com.streamfusion.platform.access.service.impl;

import com.baomidou.mybatisplus.spring.service.impl.ServiceImpl;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.pojo.dto.AccessSnapshotDto;
import com.streamfusion.platform.access.pojo.vo.RoleVo;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.role.pojo.entity.RoleEntity;
import java.time.LocalDateTime;
import java.util.List;
import org.springframework.stereotype.Service;

@Service
public class AccessServiceImpl extends ServiceImpl<AccessMapper, RoleEntity>
        implements AccessService {
    public AccessServiceImpl(AccessMapper mapper) {
        this.baseMapper = mapper;
    }

    @Override
    public AccessSnapshotDto snapshot(long userId) {
        List<RoleVo> roles = baseMapper.findRoles(userId);
        boolean superAdmin = roles.stream().anyMatch(role -> "SUPER_ADMIN".equals(role.getCode()));
        List<String> modules = baseMapper.findModules(userId);
        return new AccessSnapshotDto(roles, modules, superAdmin);
    }

    @Override
    public boolean hasModuleAccess(long userId, String module) {
        return module != null && !module.isBlank() && baseMapper.hasModuleAccess(userId, module);
    }

    @Override
    public boolean isSuperAdmin(long userId) {
        return baseMapper.isSuperAdmin(userId);
    }

    @Override
    public void bindAllPages(long roleId, LocalDateTime now) {
        baseMapper.bindAllPages(roleId, now);
    }
}
