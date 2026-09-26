package com.streamfusion.platform.menu.service.impl;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.menu.mapper.MenuMapper;
import com.streamfusion.platform.menu.pojo.dto.MenuTreeQueryDto;
import com.streamfusion.platform.menu.pojo.dto.MenuUpdateDto;
import com.streamfusion.platform.menu.pojo.dto.MenuWriteDto;
import com.streamfusion.platform.menu.pojo.entity.MenuEntity;
import com.streamfusion.platform.menu.pojo.vo.MenuNodeVo;
import com.streamfusion.platform.menu.pojo.vo.MenuPageVo;
import com.streamfusion.platform.menu.pojo.vo.MenuRouteVo;
import com.streamfusion.platform.menu.service.MenuRules;
import com.streamfusion.platform.menu.service.MenuService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
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

/** Menu persistence and bounded tree rules. Role/menu authorization is kept in access. */
@Service
@RequiredArgsConstructor
public class MenuServiceImpl implements MenuService {
    private static final int MAX_NODES = 1000;
    private static final int MAX_DEPTH = 5;
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

    private final MenuMapper menus;
    private final MenuRules rules;
    private final AccessMapper access;
    private final CurrentUserService currentUser;
    private final ModuleAuthorizationService authorization;
    private final AuditService audit;
    private final Clock clock;

    private record Actor(long userId, long superAdminRoleId) {}

    @Override
    @Transactional(readOnly = true)
    public List<MenuNodeVo> tree(MenuTreeQueryDto input) {
        MenuTreeQueryDto query = rules.query(input);
        List<MenuEntity> all = load();
        Map<Long, List<MenuEntity>> children = children(all);
        List<MenuNodeVo> roots = new ArrayList<>();
        for (MenuEntity root : children.getOrDefault(null, List.of())) {
            MenuNodeVo matched = filtered(root, children, query);
            if (matched != null) roots.add(matched);
        }
        return List.copyOf(roots);
    }

    @Override
    @Transactional(readOnly = true)
    public List<MenuRouteVo> routes(long userId) {
        Set<Long> authorized = Set.copyOf(menus.authorizedPageIds(userId));
        if (authorized.isEmpty()) return List.of();
        Map<Long, List<MenuEntity>> children = children(load());
        List<MenuRouteVo> roots = new ArrayList<>();
        for (MenuEntity root : children.getOrDefault(null, List.of())) {
            MenuRouteVo route = published(root, children, authorized);
            if (route != null) roots.add(route);
        }
        return List.copyOf(roots);
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public MenuNodeVo create(MenuWriteDto input, AuditContextDto context) {
        MenuRules.Values values = rules.write(input);
        Actor actor = lockAndRequireActor();
        long actorId = actor.userId();
        List<MenuEntity> all = load();
        if (all.size() >= MAX_NODES) throw BusinessException.error(ErrorCode.CONFLICT);
        Map<Long, MenuEntity> byId = byId(all);
        validatePlacement(values.parentId(), null, byId, children(all));
        uniqueRoute(values, null, all);
        LocalDateTime now = now();
        MenuEntity menu = new MenuEntity();
        fill(menu, values);
        menu.setVersion(0L);
        menu.setCreatedAt(now);
        menu.setUpdatedAt(now);
        menu.setCreatedBy(actorId);
        menu.setUpdatedBy(actorId);
        try {
            if (menus.insert(menu) != 1) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
            if ("PAGE".equals(menu.getType())
                    && menus.bindRole(actor.superAdminRoleId(), menu.getId(), now, actorId) != 1) {
                throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
            }
        } catch (DataIntegrityViolationException ex) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        audit.record(
                actorId,
                "MENU",
                menu.getId(),
                "MENU_CREATE",
                "SUCCESS",
                null,
                context,
                Map.of("name", menu.getName()));
        return view(menu.getId());
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public MenuNodeVo update(String id, MenuUpdateDto input, AuditContextDto context) {
        long menuId = rules.id(id);
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        long version = rules.version(input.getVersion());
        MenuRules.Values values = rules.write(input);
        long actorId = lockAndRequireActor().userId();
        List<MenuEntity> all = load();
        Map<Long, MenuEntity> byId = byId(all);
        MenuEntity current = byId.get(menuId);
        if (current == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (current.getVersion() != version || version == Long.MAX_VALUE) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        if (!current.getType().equals(values.type()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        if ("PAGE".equals(current.getType())
                && !current.getModuleKey().equals(values.moduleKey())) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        validatePlacement(values.parentId(), menuId, byId, children(all));
        uniqueRoute(values, menuId, all);
        var change =
                new LambdaUpdateWrapper<MenuEntity>()
                        .eq(MenuEntity::getId, menuId)
                        .eq(MenuEntity::getVersion, version)
                        .set(MenuEntity::getParentId, values.parentId())
                        .set(MenuEntity::getName, values.name())
                        .set(MenuEntity::getRouteName, values.routeName())
                        .set(MenuEntity::getPath, values.path())
                        .set(MenuEntity::getComponentKey, values.componentKey())
                        .set(MenuEntity::getModuleKey, values.moduleKey())
                        .set(MenuEntity::getIcon, values.icon())
                        .set(MenuEntity::getSortOrder, values.sortOrder())
                        .set(MenuEntity::getVisible, values.visible())
                        .set(MenuEntity::getEnabled, values.enabled())
                        .set(MenuEntity::getVersion, version + 1)
                        .set(MenuEntity::getUpdatedAt, now())
                        .set(MenuEntity::getUpdatedBy, actorId);
        try {
            if (menus.update(null, change) != 1) {
                throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
            }
        } catch (DataIntegrityViolationException ex) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        audit.record(
                actorId,
                "MENU",
                menuId,
                "MENU_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of("name", values.name()));
        return view(menuId);
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void delete(String id, String requestedVersion, AuditContextDto context) {
        long menuId = rules.id(id);
        long version = rules.version(requestedVersion);
        long actorId = lockAndRequireActor().userId();
        List<MenuEntity> all = load();
        MenuEntity target = byId(all).get(menuId);
        if (target == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (target.getVersion() != version) {
            throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        }
        if (!children(all).getOrDefault(menuId, List.of()).isEmpty()) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        menus.deleteRoleBindings(menuId);
        if (menus.delete(
                        new LambdaQueryWrapper<MenuEntity>()
                                .eq(MenuEntity::getId, menuId)
                                .eq(MenuEntity::getVersion, version))
                != 1) {
            throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        }
        audit.record(
                actorId,
                "MENU",
                menuId,
                "MENU_DELETE",
                "SUCCESS",
                null,
                context,
                Map.of("name", target.getName()));
    }

    private Actor lockAndRequireActor() {
        Long superAdminRole = access.lockSuperAdminRole();
        if (superAdminRole == null) throw BusinessException.error(ErrorCode.FORBIDDEN);
        long actorId = currentUser.requireNormal().getId();
        authorization.recheckCurrentRequest(actorId);
        return new Actor(actorId, superAdminRole);
    }

    private List<MenuEntity> load() {
        List<MenuEntity> all =
                menus.selectList(
                        new LambdaQueryWrapper<MenuEntity>()
                                .select(
                                        MenuEntity::getId,
                                        MenuEntity::getParentId,
                                        MenuEntity::getName,
                                        MenuEntity::getType,
                                        MenuEntity::getRouteName,
                                        MenuEntity::getPath,
                                        MenuEntity::getComponentKey,
                                        MenuEntity::getModuleKey,
                                        MenuEntity::getIcon,
                                        MenuEntity::getSortOrder,
                                        MenuEntity::getVisible,
                                        MenuEntity::getEnabled,
                                        MenuEntity::getVersion)
                                .orderByAsc(MenuEntity::getSortOrder)
                                .orderByAsc(MenuEntity::getId)
                                .last("LIMIT 1001"));
        if (all.size() > MAX_NODES) throw BusinessException.error(ErrorCode.CONFLICT);
        Map<Long, MenuEntity> byId = byId(all);
        for (MenuEntity menu : all) {
            if (menu.getParentId() != null) {
                MenuEntity parent = byId.get(menu.getParentId());
                if (parent == null || !"DIRECTORY".equals(parent.getType())) {
                    throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
                }
            }
            depth(menu.getId(), byId, new HashSet<>());
        }
        return all;
    }

    private static Map<Long, MenuEntity> byId(List<MenuEntity> all) {
        Map<Long, MenuEntity> result = new HashMap<>();
        for (MenuEntity menu : all) result.put(menu.getId(), menu);
        return result;
    }

    private static Map<Long, List<MenuEntity>> children(List<MenuEntity> all) {
        Map<Long, List<MenuEntity>> result = new HashMap<>();
        for (MenuEntity menu : all) {
            result.computeIfAbsent(menu.getParentId(), ignored -> new ArrayList<>()).add(menu);
        }
        return result;
    }

    private static int depth(long id, Map<Long, MenuEntity> byId, Set<Long> seen) {
        if (!seen.add(id)) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        MenuEntity node = byId.get(id);
        if (node == null) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        int depth = node.getParentId() == null ? 1 : 1 + depth(node.getParentId(), byId, seen);
        if (depth > MAX_DEPTH) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        return depth;
    }

    private static void validatePlacement(
            Long parentId,
            Long movingId,
            Map<Long, MenuEntity> byId,
            Map<Long, List<MenuEntity>> children) {
        MenuEntity parent = parentId == null ? null : byId.get(parentId);
        if (parentId != null && (parent == null || !"DIRECTORY".equals(parent.getType()))) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        for (Long current = parentId; current != null; current = byId.get(current).getParentId()) {
            if (current.equals(movingId)) throw BusinessException.error(ErrorCode.CONFLICT);
        }
        int top = parentId == null ? 1 : depth(parentId, byId, new HashSet<>()) + 1;
        int height = movingId == null ? 1 : height(movingId, children);
        if (top + height - 1 > MAX_DEPTH) throw BusinessException.error(ErrorCode.CONFLICT);
    }

    private static int height(long id, Map<Long, List<MenuEntity>> children) {
        int result = 1;
        for (MenuEntity child : children.getOrDefault(id, List.of())) {
            result = Math.max(result, 1 + height(child.getId(), children));
        }
        return result;
    }

    private static void uniqueRoute(MenuRules.Values values, Long currentId, List<MenuEntity> all) {
        if (!"PAGE".equals(values.type())) return;
        for (MenuEntity menu : all) {
            if (!menu.getId().equals(currentId)
                    && (values.routeName().equals(menu.getRouteName())
                            || values.path().equals(menu.getPath()))) {
                throw BusinessException.error(ErrorCode.CONFLICT);
            }
        }
    }

    private MenuNodeVo view(long id) {
        MenuEntity entity = menus.selectById(id);
        if (entity == null) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        return toVo(entity, List.of());
    }

    private static MenuNodeVo filtered(
            MenuEntity menu, Map<Long, List<MenuEntity>> children, MenuTreeQueryDto query) {
        List<MenuNodeVo> matchedChildren = new ArrayList<>();
        for (MenuEntity child : children.getOrDefault(menu.getId(), List.of())) {
            MenuNodeVo matched = filtered(child, children, query);
            if (matched != null) matchedChildren.add(matched);
        }
        boolean direct =
                (query.getName() == null || menu.getName().contains(query.getName()))
                        && (query.getType() == null || query.getType().equals(menu.getType()))
                        && (query.getEnabled() == null
                                || query.getEnabled().equals(menu.getEnabled()));
        if (!direct && matchedChildren.isEmpty()) return null;
        return toVo(menu, matchedChildren);
    }

    private static MenuRouteVo published(
            MenuEntity menu, Map<Long, List<MenuEntity>> children, Set<Long> authorized) {
        if (!menu.getEnabled()) return null;
        if ("PAGE".equals(menu.getType())) {
            if (!authorized.contains(menu.getId())) return null;
            return new MenuRouteVo(
                    Long.toString(menu.getId()),
                    menu.getName(),
                    menu.getType(),
                    menu.getIcon(),
                    menu.getVisible(),
                    menu.getRouteName(),
                    menu.getPath(),
                    menu.getComponentKey(),
                    null);
        }
        List<MenuRouteVo> descendants = new ArrayList<>();
        for (MenuEntity child : children.getOrDefault(menu.getId(), List.of())) {
            MenuRouteVo route = published(child, children, authorized);
            if (route != null) descendants.add(route);
        }
        return descendants.isEmpty()
                ? null
                : new MenuRouteVo(
                        Long.toString(menu.getId()),
                        menu.getName(),
                        menu.getType(),
                        menu.getIcon(),
                        menu.getVisible(),
                        null,
                        null,
                        null,
                        List.copyOf(descendants));
    }

    private static MenuNodeVo toVo(MenuEntity menu, List<MenuNodeVo> children) {
        boolean directory = "DIRECTORY".equals(menu.getType());
        return new MenuNodeVo(
                Long.toString(menu.getId()),
                menu.getParentId() == null ? null : Long.toString(menu.getParentId()),
                menu.getName(),
                menu.getType(),
                menu.getIcon(),
                menu.getSortOrder(),
                menu.getVisible(),
                menu.getEnabled(),
                Long.toString(menu.getVersion()),
                directory
                        ? null
                        : new MenuPageVo(
                                menu.getRouteName(),
                                menu.getPath(),
                                menu.getComponentKey(),
                                menu.getModuleKey()),
                directory ? List.copyOf(children) : null);
    }

    private static void fill(MenuEntity menu, MenuRules.Values values) {
        menu.setParentId(values.parentId());
        menu.setName(values.name());
        menu.setType(values.type());
        menu.setRouteName(values.routeName());
        menu.setPath(values.path());
        menu.setComponentKey(values.componentKey());
        menu.setModuleKey(values.moduleKey());
        menu.setIcon(values.icon());
        menu.setSortOrder(values.sortOrder());
        menu.setVisible(values.visible());
        menu.setEnabled(values.enabled());
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), BUSINESS_ZONE);
    }
}
