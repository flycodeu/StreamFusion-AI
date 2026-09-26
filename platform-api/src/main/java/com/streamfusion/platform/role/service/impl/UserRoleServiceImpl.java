package com.streamfusion.platform.role.service.impl;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.pojo.vo.RoleVo;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.menu.pojo.vo.MenuRouteVo;
import com.streamfusion.platform.menu.service.MenuService;
import com.streamfusion.platform.role.mapper.RoleMapper;
import com.streamfusion.platform.role.pojo.dto.UserRolesUpdateDto;
import com.streamfusion.platform.role.pojo.entity.RoleEntity;
import com.streamfusion.platform.role.pojo.vo.RoleOptionVo;
import com.streamfusion.platform.role.pojo.vo.UserRolesVo;
import com.streamfusion.platform.role.service.RoleRules;
import com.streamfusion.platform.role.service.UserRoleService;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class UserRoleServiceImpl implements UserRoleService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

    private final UserService users;
    private final RoleMapper roles;
    private final RoleRules rules;
    private final MenuService menus;
    private final AccessMapper accessMapper;
    private final AccessService access;
    private final CurrentUserService currentUser;
    private final ModuleAuthorizationService authorization;
    private final AuditService audit;
    private final Clock clock;

    @Override
    @Transactional(readOnly = true)
    public UserRolesVo get(String userId) {
        long id = rules.id(userId);
        UserEntity user = requireUser(id);
        return view(user);
    }

    @Override
    @Transactional(readOnly = true)
    public List<RoleOptionVo> options() {
        long actorId = currentUser.requireNormal().getId();
        boolean superAdmin = access.isSuperAdmin(actorId);
        var query =
                new LambdaQueryWrapper<RoleEntity>()
                        .select(RoleEntity::getId, RoleEntity::getCode, RoleEntity::getName)
                        .eq(RoleEntity::getStatus, "ENABLED")
                        .orderByAsc(RoleEntity::getCode);
        if (!superAdmin) query.ne(RoleEntity::getCode, "SUPER_ADMIN");
        return roles.selectList(query).stream()
                .map(
                        role ->
                                new RoleOptionVo(
                                        Long.toString(role.getId()),
                                        role.getCode(),
                                        role.getName()))
                .toList();
    }

    @Override
    @Transactional(readOnly = true)
    public List<MenuRouteVo> routes(String userId) {
        long id = rules.id(userId);
        requireUser(id);
        return menus.routes(id);
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public UserRolesVo update(String userId, UserRolesUpdateDto input, AuditContextDto context) {
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        long id = rules.id(userId);
        long version = rules.version(input.getVersion());
        List<Long> selected = rules.ids(input.getRoleIds(), "roleIds");
        Long superAdminRoleId = accessMapper.lockSuperAdminRole();
        if (superAdminRoleId == null) throw BusinessException.error(ErrorCode.FORBIDDEN);
        long actorId = currentUser.requireNormal().getId();
        authorization.recheckCurrentRequest(actorId);
        boolean superAdmin = access.isSuperAdmin(actorId);
        UserEntity target = users.lockById(id);
        if (target == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if ((access.isSuperAdmin(id) && !superAdmin)
                || (actorId == id && superAdmin && !selected.contains(superAdminRoleId))) {
            throw BusinessException.error(ErrorCode.PROTECTED_ACCOUNT);
        }
        if (!target.getVersion().equals(version)) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        com.streamfusion.platform.common.validation.VersionCounter.requireIncrementable(version);
        List<Long> previous = roles.userRoleIds(id);
        Set<Long> previousIds = new HashSet<>(previous);
        for (Long roleId : selected)
            requireAssignable(roleId, superAdmin, previousIds.contains(roleId));
        if (new HashSet<>(previous).equals(new HashSet<>(selected))) return view(target);
        roles.deleteUserRoles(id);
        LocalDateTime now = now();
        for (Long roleId : selected) roles.insertUserRole(id, roleId, now, actorId);
        if (!users.update(
                null,
                new LambdaUpdateWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getVersion, version)
                        .setIncrBy(UserEntity::getVersion, 1)
                        .set(UserEntity::getUpdatedAt, now)
                        .set(UserEntity::getUpdatedBy, actorId)))
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        audit.record(
                actorId,
                "USER",
                id,
                "USER_ROLE_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "username",
                        target.getUsername(),
                        "beforeRoleIds",
                        previous,
                        "afterRoleIds",
                        selected));
        return view(requireUser(id));
    }

    private void requireAssignable(long roleId, boolean superAdmin, boolean alreadyAssigned) {
        RoleEntity role = roles.selectById(roleId);
        if (role == null || (!alreadyAssigned && !"ENABLED".equals(role.getStatus()))) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        if ("SUPER_ADMIN".equals(role.getCode()) && !superAdmin)
            throw BusinessException.error(ErrorCode.FORBIDDEN);
    }

    private UserEntity requireUser(long id) {
        UserEntity user = users.getById(id);
        if (user == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return user;
    }

    private UserRolesVo view(UserEntity user) {
        List<RoleVo> assigned =
                roles.assignedRoles(user.getId()).stream()
                        .map(
                                role ->
                                        new RoleVo(
                                                Long.toString(role.getId()),
                                                role.getCode(),
                                                role.getName()))
                        .toList();
        return new UserRolesVo(
                Long.toString(user.getId()), Long.toString(user.getVersion()), assigned);
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), BUSINESS_ZONE);
    }
}
