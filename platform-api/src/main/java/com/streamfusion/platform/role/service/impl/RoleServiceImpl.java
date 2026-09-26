package com.streamfusion.platform.role.service.impl;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.menu.pojo.vo.MenuNodeVo;
import com.streamfusion.platform.menu.service.MenuService;
import com.streamfusion.platform.role.mapper.RoleMapper;
import com.streamfusion.platform.role.pojo.dto.RoleCreateDto;
import com.streamfusion.platform.role.pojo.dto.RoleMenusUpdateDto;
import com.streamfusion.platform.role.pojo.dto.RoleQueryDto;
import com.streamfusion.platform.role.pojo.dto.RoleUpdateDto;
import com.streamfusion.platform.role.pojo.entity.RoleEntity;
import com.streamfusion.platform.role.pojo.vo.RoleDetailVo;
import com.streamfusion.platform.role.pojo.vo.RoleMenuNodeVo;
import com.streamfusion.platform.role.pojo.vo.RoleMenusVo;
import com.streamfusion.platform.role.pojo.vo.RoleOptionVo;
import com.streamfusion.platform.role.service.RoleRules;
import com.streamfusion.platform.role.service.RoleService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.dao.DataIntegrityViolationException;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class RoleServiceImpl implements RoleService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

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
    public PageResultVo<RoleDetailVo> page(RoleQueryDto input) {
        RoleQueryDto query = rules.query(input);
        var filter =
                new LambdaQueryWrapper<RoleEntity>()
                        .select(
                                RoleEntity::getId,
                                RoleEntity::getCode,
                                RoleEntity::getName,
                                RoleEntity::getDescription,
                                RoleEntity::getStatus,
                                RoleEntity::getVersion);
        if (query.getStatus() != null) filter.eq(RoleEntity::getStatus, query.getStatus());
        if (query.getKeyword() != null) {
            String search =
                    query.getKeyword().replace("!", "!!").replace("%", "!%").replace("_", "!_");
            filter.apply(
                    "(LOWER(code) LIKE CONCAT('%', LOWER({0}), '%') ESCAPE '!'"
                            + " OR LOWER(name) LIKE CONCAT('%', LOWER({0}), '%') ESCAPE '!')",
                    search);
        }
        filter.orderByAsc(RoleEntity::getId);
        var result = roles.selectPage(query.toPage(), filter);
        return PageResultVo.from(
                result, result.getRecords().stream().map(RoleServiceImpl::view).toList());
    }

    @Override
    @Transactional(readOnly = true)
    public List<RoleOptionVo> options() {
        boolean superAdmin = access.isSuperAdmin(currentUser.requireNormal().getId());
        return roles
                .selectList(
                        new LambdaQueryWrapper<RoleEntity>()
                                .select(RoleEntity::getId, RoleEntity::getCode, RoleEntity::getName)
                                .eq(RoleEntity::getStatus, "ENABLED")
                                .ne(!superAdmin, RoleEntity::getCode, "SUPER_ADMIN")
                                .orderByAsc(RoleEntity::getCode))
                .stream()
                .map(
                        role ->
                                new RoleOptionVo(
                                        Long.toString(role.getId()),
                                        role.getCode(),
                                        role.getName()))
                .toList();
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public RoleDetailVo create(RoleCreateDto input, AuditContextDto context) {
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        String code = rules.code(input.getCode());
        String name = rules.name(input.getName());
        String description = rules.description(input.getDescription());
        long actorId = lockActor();
        LocalDateTime now = now();
        RoleEntity role = new RoleEntity();
        role.setCode(code);
        role.setName(name);
        role.setDescription(description);
        role.setStatus("ENABLED");
        role.setVersion(0L);
        role.setCreatedAt(now);
        role.setUpdatedAt(now);
        role.setCreatedBy(actorId);
        role.setUpdatedBy(actorId);
        try {
            if (roles.insert(role) != 1) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        } catch (DataIntegrityViolationException ex) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        audit.record(
                actorId,
                "ROLE",
                role.getId(),
                "ROLE_CREATE",
                "SUCCESS",
                null,
                context,
                Map.of("code", code, "name", name));
        return view(role);
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public RoleDetailVo update(String id, RoleUpdateDto input, AuditContextDto context) {
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        long roleId = rules.id(id);
        long version = rules.version(input.getVersion());
        String name = rules.name(input.getName());
        String description = rules.description(input.getDescription());
        long actorId = lockActor();
        RoleEntity role = requireEditable(roleId, version, ErrorCode.VERSION_CONFLICT);
        var change =
                updateVersion(role, actorId)
                        .set(RoleEntity::getName, name)
                        .set(RoleEntity::getDescription, description);
        if (roles.update(null, change) != 1)
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        audit.record(
                actorId,
                "ROLE",
                roleId,
                "ROLE_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of("code", role.getCode(), "name", name));
        return view(requireRole(roleId));
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public RoleDetailVo changeStatus(
            String id, String requestedVersion, boolean enabled, AuditContextDto context) {
        long roleId = rules.id(id);
        long version = rules.version(requestedVersion);
        long actorId = lockActor();
        RoleEntity role = requireEditable(roleId, version, ErrorCode.VERSION_CONFLICT);
        String status = enabled ? "ENABLED" : "DISABLED";
        if (status.equals(role.getStatus())) return view(role);
        if (roles.update(null, updateVersion(role, actorId).set(RoleEntity::getStatus, status))
                != 1) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        audit.record(
                actorId,
                "ROLE",
                roleId,
                enabled ? "ROLE_ENABLE" : "ROLE_DISABLE",
                "SUCCESS",
                null,
                context,
                Map.of("code", role.getCode(), "name", role.getName()));
        return view(requireRole(roleId));
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void delete(String id, String requestedVersion, AuditContextDto context) {
        long roleId = rules.id(id);
        long version = rules.version(requestedVersion);
        long actorId = lockActor();
        RoleEntity role = requireEditable(roleId, version, ErrorCode.PRECONDITION_FAILED);
        if (roles.userCount(roleId) > 0) throw BusinessException.error(ErrorCode.CONFLICT);
        roles.deleteMenus(roleId);
        if (roles.delete(
                        new LambdaQueryWrapper<RoleEntity>()
                                .eq(RoleEntity::getId, roleId)
                                .eq(RoleEntity::getVersion, version))
                != 1) throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        audit.record(
                actorId,
                "ROLE",
                roleId,
                "ROLE_DELETE",
                "SUCCESS",
                null,
                context,
                Map.of("code", role.getCode(), "name", role.getName()));
    }

    @Override
    @Transactional(readOnly = true)
    public RoleMenusVo menus(String id) {
        long roleId = rules.id(id);
        RoleEntity role = requireRole(roleId);
        return menuView(role);
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public RoleMenusVo updateMenus(String id, RoleMenusUpdateDto input, AuditContextDto context) {
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        long roleId = rules.id(id);
        long version = rules.version(input.getVersion());
        List<Long> requested = rules.ids(input.getMenuIds(), "menuIds");
        long actorId = lockActor();
        RoleEntity role = requireEditable(roleId, version, ErrorCode.VERSION_CONFLICT);
        List<MenuNodeVo> tree = menus.tree(null);
        Map<Long, MenuNodeVo> byId = new HashMap<>();
        for (MenuNodeVo root : tree) index(root, byId);
        List<Long> previous = roles.menuIds(roleId);
        Set<Long> previousIds = new HashSet<>(previous);
        Set<Long> pages = new HashSet<>();
        for (Long selected : requested) {
            MenuNodeVo node = byId.get(selected);
            if (node == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
            if ("PAGE".equals(node.type()) && previousIds.contains(selected)) {
                pages.add(selected);
                continue;
            }
            if (!active(node, byId)) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
            collectPages(node, pages);
        }
        roles.deleteMenus(roleId);
        LocalDateTime now = now();
        for (Long pageId : pages.stream().sorted().toList()) {
            roles.insertMenu(roleId, pageId, now, actorId);
        }
        if (roles.update(null, updateVersion(role, actorId)) != 1) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        audit.record(
                actorId,
                "ROLE",
                roleId,
                "ROLE_MENU_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "code",
                        role.getCode(),
                        "name",
                        role.getName(),
                        "beforeMenuIds",
                        previous,
                        "afterMenuIds",
                        pages.stream().sorted().toList()));
        return menuView(requireRole(roleId));
    }

    private long lockActor() {
        if (accessMapper.lockSuperAdminRole() == null)
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        long actorId = currentUser.requireNormal().getId();
        authorization.recheckCurrentRequest(actorId);
        return actorId;
    }

    private RoleEntity requireRole(long id) {
        RoleEntity role = roles.selectById(id);
        if (role == null) {
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        }
        return role;
    }

    private RoleEntity requireEditable(long id, long version, ErrorCode versionError) {
        RoleEntity role = requireRole(id);
        if ("SUPER_ADMIN".equals(role.getCode()))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        if (!role.getVersion().equals(version) || version == Long.MAX_VALUE) {
            throw BusinessException.error(versionError);
        }
        return role;
    }

    private LambdaUpdateWrapper<RoleEntity> updateVersion(RoleEntity role, long actorId) {
        return new LambdaUpdateWrapper<RoleEntity>()
                .eq(RoleEntity::getId, role.getId())
                .eq(RoleEntity::getVersion, role.getVersion())
                .setIncrBy(RoleEntity::getVersion, 1)
                .set(RoleEntity::getUpdatedAt, now())
                .set(RoleEntity::getUpdatedBy, actorId);
    }

    private RoleMenusVo menuView(RoleEntity role) {
        List<RoleMenuNodeVo> tree =
                menus.tree(null).stream().map(RoleServiceImpl::menuNode).toList();
        List<String> selected = roles.menuIds(role.getId()).stream().map(String::valueOf).toList();
        return new RoleMenusVo(
                Long.toString(role.getId()), Long.toString(role.getVersion()), selected, tree);
    }

    private static RoleMenuNodeVo menuNode(MenuNodeVo node) {
        return new RoleMenuNodeVo(
                node.id(),
                node.name(),
                node.type(),
                node.enabled(),
                node.children() == null
                        ? null
                        : node.children().stream().map(RoleServiceImpl::menuNode).toList());
    }

    private static void index(MenuNodeVo node, Map<Long, MenuNodeVo> byId) {
        byId.put(Long.parseLong(node.id()), node);
        if (node.children() != null) for (MenuNodeVo child : node.children()) index(child, byId);
    }

    private static boolean active(MenuNodeVo node, Map<Long, MenuNodeVo> byId) {
        for (MenuNodeVo current = node;
                current != null;
                current =
                        current.parentId() == null
                                ? null
                                : byId.get(Long.parseLong(current.parentId()))) {
            if (!current.enabled()) return false;
        }
        return true;
    }

    private static void collectPages(MenuNodeVo node, Set<Long> pages) {
        if (!node.enabled()) return;
        if ("PAGE".equals(node.type())) {
            pages.add(Long.parseLong(node.id()));
        } else if (node.children() != null) {
            for (MenuNodeVo child : node.children()) collectPages(child, pages);
        }
    }

    private static RoleDetailVo view(RoleEntity role) {
        return new RoleDetailVo(
                Long.toString(role.getId()),
                role.getCode(),
                role.getName(),
                role.getDescription(),
                role.getStatus(),
                Long.toString(role.getVersion()));
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), BUSINESS_ZONE);
    }
}
