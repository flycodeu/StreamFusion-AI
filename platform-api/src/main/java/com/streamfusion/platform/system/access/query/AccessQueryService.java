package com.streamfusion.platform.system.access.query;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.common.security.ModuleRegistry;
import com.streamfusion.platform.common.validation.DecimalInput;
import com.streamfusion.platform.system.access.query.mapper.AccessQueryMapper;
import com.streamfusion.platform.system.access.service.AccessService;
import com.streamfusion.platform.system.auth.service.CurrentUserService;
import com.streamfusion.platform.system.department.service.DepartmentService;
import com.streamfusion.platform.system.menu.mapper.MenuMapper;
import com.streamfusion.platform.system.menu.pojo.entity.MenuEntity;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Function;
import java.util.stream.Collectors;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
@Transactional(readOnly = true, isolation = Isolation.REPEATABLE_READ)
public class AccessQueryService {
    private final AccessQueryMapper queries;
    private final MenuMapper menus;
    private final ModuleRegistry modules;
    private final AccessService access;
    private final CurrentUserService current;
    private final ModuleAuthorizationService authorization;
    private final DepartmentService departments;
    private final Clock clock;

    public record ExportFile(String filename, byte[] bytes) {}

    public ExportFile exportPages(String keyword) {
        AccessQueryDto input = new AccessQueryDto();
        input.setKeyword(keyword);
        Query query = validate(input);
        List<AccessQueryViews.PageInfo> selected =
                pageMap().values().stream()
                        .filter(
                                page ->
                                        matches(page.menuPath(), query.term())
                                                || matches(page.path(), query.term()))
                        .sorted(
                                java.util.Comparator.comparing(AccessQueryViews.PageInfo::menuPath)
                                        .thenComparing(AccessQueryViews.PageInfo::id))
                        .toList();
        List<AccessQueryViews.PageInfo> effectivePages =
                selected.stream().filter(AccessQueryService::pageEffective).toList();
        List<AccessQueryRows.ExportAssignmentRow> assignments =
                effectivePages.isEmpty()
                        ? List.of()
                        : queries.exportAssignments(
                                effectivePages.stream()
                                        .map(page -> Long.parseLong(page.id()))
                                        .toList());
        AccessQueryWorkbook.requireAssignmentLimit(assignments.size());
        Map<Long, AccessQueryViews.UserIdentity> people = identityMap(assignments);
        Map<Long, List<AccessQueryViews.UserIdentity>> byPage = new HashMap<>();
        for (var assignment : assignments) {
            byPage.computeIfAbsent(assignment.getMenuId(), ignored -> new ArrayList<>())
                    .add(people.get(assignment.getId()));
        }
        List<AccessQueryWorkbook.PageRow> rows =
                selected.stream()
                        .map(
                                page ->
                                        new AccessQueryWorkbook.PageRow(
                                                page,
                                                byPage.getOrDefault(
                                                        Long.parseLong(page.id()), List.of())))
                        .toList();
        String filename =
                "access-pages-"
                        + java.time.format.DateTimeFormatter.ofPattern("yyyyMMdd-HHmmss")
                                .withZone(ZoneId.of("Asia/Shanghai"))
                                .format(clock.instant())
                        + ".xlsx";
        return new ExportFile(filename, AccessQueryWorkbook.write(rows));
    }

    public AccessQueryViews.Result<AccessQueryViews.UserIdentity> users(AccessQueryDto input) {
        Query query = validate(input);
        return result(
                identities(queries.users(query.keyword(), query.offset(), query.size())),
                query,
                queries.userCount(query.keyword()),
                null,
                null);
    }

    public AccessQueryViews.Result<AccessQueryViews.PageInfo> pages(AccessQueryDto input) {
        Query query = validate(input);
        List<AccessQueryViews.PageInfo> matches =
                pageMap().values().stream()
                        .filter(
                                page ->
                                        matches(page.menuPath(), query.term())
                                                || matches(page.path(), query.term()))
                        .sorted(
                                java.util.Comparator.comparing(AccessQueryViews.PageInfo::menuPath)
                                        .thenComparing(AccessQueryViews.PageInfo::id))
                        .toList();
        return result(slice(matches, query), query, matches.size(), null, null);
    }

    public AccessQueryViews.Result<AccessQueryViews.RoleOverview> roles(AccessQueryDto input) {
        Query query = validate(input);
        Map<Long, AccessQueryViews.PageInfo> pages = pageMap();
        var rows =
                queries.roles(query.keyword(), query.offset(), query.size()).stream()
                        .map(
                                row -> {
                                    long effectivePages =
                                            "ENABLED".equals(row.getStatus())
                                                    ? queries.rolePageIds(row.getId()).stream()
                                                            .filter(
                                                                    id ->
                                                                            pageEffective(
                                                                                    pages.get(id)))
                                                            .count()
                                                    : 0;
                                    return new AccessQueryViews.RoleOverview(
                                            role(row),
                                            row.getUserCount(),
                                            row.getUsableUserCount(),
                                            row.getPageCount(),
                                            effectivePages);
                                })
                        .toList();
        return result(rows, query, queries.roleCount(query.keyword()), null, null);
    }

    public AccessQueryViews.Result<AccessQueryViews.RolePage> rolePages(
            String roleId, AccessQueryDto input) {
        Query query = validate(input);
        long id = DecimalInput.id(roleId, "roleId");
        AccessQueryRows.RoleRow role = requireRole(id);
        Map<Long, AccessQueryViews.PageInfo> pages = pageMap();
        List<Long> assigned = queries.rolePageIds(id);
        if (assigned.size() > 1000) throw BusinessException.error(ErrorCode.CONFLICT);
        var matches =
                assigned.stream()
                        .map(pages::get)
                        .filter(java.util.Objects::nonNull)
                        .filter(
                                page ->
                                        matches(page.menuPath(), query.term())
                                                || matches(page.path(), query.term()))
                        .sorted(
                                java.util.Comparator.comparing(AccessQueryViews.PageInfo::menuPath)
                                        .thenComparing(AccessQueryViews.PageInfo::id))
                        .toList();
        var rows =
                slice(matches, query).stream()
                        .map(
                                page -> {
                                    var blockers = new ArrayList<>(page.blockers());
                                    if (!"ENABLED".equals(role.getStatus()))
                                        blockers.add(
                                                new AccessQueryViews.Blocker(
                                                        "ROLE_DISABLED",
                                                        role.getId().toString(),
                                                        role.getName()));
                                    return new AccessQueryViews.RolePage(
                                            page,
                                            pageEffective(page)
                                                    && "ENABLED".equals(role.getStatus()),
                                            List.copyOf(blockers));
                                })
                        .toList();
        return result(rows, query, matches.size(), null, null);
    }

    public AccessQueryViews.Result<AccessQueryViews.UserIdentity> roleUsers(
            String roleId, AccessQueryDto input) {
        Query query = validate(input);
        long id = DecimalInput.id(roleId, "roleId");
        requireRole(id);
        var rows = identities(queries.roleUsers(id, query.keyword(), query.offset(), query.size()));
        return result(rows, query, queries.roleUserCount(id, query.keyword()), null, null);
    }

    public AccessQueryViews.Result<AccessQueryViews.PageOverview> pageOverview(
            AccessQueryDto input) {
        Query query = validate(input);
        var matches =
                pageMap().values().stream()
                        .filter(
                                page ->
                                        matches(page.menuPath(), query.term())
                                                || matches(page.path(), query.term()))
                        .sorted(
                                java.util.Comparator.comparing(AccessQueryViews.PageInfo::menuPath)
                                        .thenComparing(AccessQueryViews.PageInfo::id))
                        .toList();
        var selected = slice(matches, query);
        Map<Long, AccessQueryRows.PageCountsRow> counts =
                selected.isEmpty()
                        ? Map.of()
                        : queries
                                .pageCounts(
                                        selected.stream()
                                                .map(page -> Long.parseLong(page.id()))
                                                .toList())
                                .stream()
                                .collect(
                                        Collectors.toMap(
                                                AccessQueryRows.PageCountsRow::getMenuId,
                                                Function.identity()));
        var rows =
                selected.stream()
                        .map(
                                page -> {
                                    var count = counts.get(Long.parseLong(page.id()));
                                    return new AccessQueryViews.PageOverview(
                                            page,
                                            count == null ? 0 : count.getRoleCount(),
                                            count == null || !pageEffective(page)
                                                    ? 0
                                                    : count.getActiveRoleCount(),
                                            count == null ? 0 : count.getUserCount(),
                                            count == null || !pageEffective(page)
                                                    ? 0
                                                    : count.getEligibleUserCount());
                                })
                        .toList();
        return result(rows, query, matches.size(), null, null);
    }

    public AccessQueryViews.Result<AccessQueryViews.UserPage> userPages(
            String userId, AccessQueryDto input) {
        Query query = validate(input);
        long uid = DecimalInput.id(userId, "userId");
        AccessQueryViews.UserIdentity subject = requireUser(uid);
        Map<Long, AccessQueryViews.PageInfo> pages = pageMap();
        List<AccessQueryRows.GrantRow> grants = queries.userGrants(uid);
        if (grants.size() > 1000) throw BusinessException.error(ErrorCode.CONFLICT);
        List<AccessQueryRows.GrantRow> matches =
                grants.stream()
                        .filter(
                                grant -> {
                                    var page = pages.get(grant.getMenuId());
                                    return page != null
                                            && (input.getModuleKey() == null
                                                    || input.getModuleKey()
                                                            .equals(page.moduleKey()))
                                            && (matches(page.menuPath(), query.term())
                                                    || matches(page.path(), query.term()))
                                            && (input.isIncludeInactive()
                                                    || (subject.accountUsable()
                                                            && effective(
                                                                    page,
                                                                    grant.getActiveSourceCount())));
                                })
                        .sorted(
                                java.util.Comparator.comparing(
                                        grant -> pages.get(grant.getMenuId()).menuPath()))
                        .toList();
        List<AccessQueryRows.GrantRow> selected = slice(matches, query);
        Map<Long, List<AccessQueryViews.RoleIdentity>> sources =
                selected.isEmpty()
                        ? Map.of()
                        : sources(
                                queries.userPageSources(
                                        uid,
                                        selected.stream()
                                                .map(AccessQueryRows.GrantRow::getMenuId)
                                                .toList()));
        Set<String> grantedModules = new HashSet<>(access.snapshot(uid).getModules());
        List<AccessQueryViews.UserPage> rows =
                selected.stream()
                        .map(
                                grant -> {
                                    var page = pages.get(grant.getMenuId());
                                    boolean effective =
                                            effective(page, grant.getActiveSourceCount());
                                    return new AccessQueryViews.UserPage(
                                            page,
                                            effective,
                                            subject.accountUsable(),
                                            effective && subject.accountUsable(),
                                            grantedModules.contains(page.moduleKey()),
                                            grant.getSourceCount(),
                                            sources.getOrDefault(grant.getMenuId(), List.of()),
                                            blockers(
                                                    page,
                                                    subject,
                                                    grant.getSourceCount(),
                                                    grant.getActiveSourceCount()));
                                })
                        .toList();
        return result(rows, query, matches.size(), subject, null);
    }

    public AccessQueryViews.Result<AccessQueryViews.PageUser> pageUsers(
            String pageId, AccessQueryDto input) {
        Query query = validate(input);
        long mid = DecimalInput.id(pageId, "pageId");
        var page = requirePage(mid);
        Long uid = input.getUserId() == null ? null : DecimalInput.id(input.getUserId(), "userId");
        var subject = uid == null ? null : requireUser(uid);
        if (!input.isIncludeInactive() && !pageEffective(page))
            return result(List.of(), query, 0, subject, page);
        List<AccessQueryRows.UserRow> selected =
                queries.pageUsers(
                        mid,
                        uid,
                        query.keyword(),
                        input.isIncludeInactive(),
                        query.offset(),
                        query.size());
        Map<Long, AccessQueryViews.UserIdentity> identities = identityMap(selected);
        Map<Long, List<AccessQueryViews.RoleIdentity>> sources =
                selected.isEmpty()
                        ? Map.of()
                        : sources(
                                queries.pageUserSources(
                                        mid,
                                        selected.stream()
                                                .map(AccessQueryRows.UserRow::getId)
                                                .toList()));
        Set<Long> moduleUsers =
                selected.isEmpty() || !page.moduleRegistered()
                        ? Set.of()
                        : Set.copyOf(
                                queries.moduleUsers(
                                        page.moduleKey(),
                                        selected.stream()
                                                .map(AccessQueryRows.UserRow::getId)
                                                .toList()));
        var rows =
                selected.stream()
                        .map(
                                row -> {
                                    var identity = identities.get(row.getId());
                                    boolean effective = effective(page, row.getActiveSourceCount());
                                    return new AccessQueryViews.PageUser(
                                            identity,
                                            effective,
                                            effective && identity.accountUsable(),
                                            moduleUsers.contains(row.getId()),
                                            row.getSourceCount(),
                                            sources.getOrDefault(row.getId(), List.of()),
                                            blockers(
                                                    page,
                                                    identity,
                                                    row.getSourceCount(),
                                                    row.getActiveSourceCount()));
                                })
                        .toList();
        return result(
                rows,
                query,
                queries.pageUserCount(mid, uid, query.keyword(), input.isIncludeInactive()),
                subject,
                page);
    }

    public AccessQueryViews.Result<AccessQueryViews.PageRole> pageRoles(
            String pageId, AccessQueryDto input) {
        Query query = validate(input);
        long mid = DecimalInput.id(pageId, "pageId");
        var page = requirePage(mid);
        Long uid = input.getUserId() == null ? null : DecimalInput.id(input.getUserId(), "userId");
        var subject = uid == null ? null : requireUser(uid);
        var inspection = uid == null ? null : inspect(mid, uid, page, subject);
        if (!input.isIncludeInactive() && !pageEffective(page))
            return result(List.of(), query, 0, subject, page, inspection);
        var rows =
                queries
                        .pageRoles(
                                mid,
                                uid,
                                query.keyword(),
                                input.isIncludeInactive(),
                                query.offset(),
                                query.size())
                        .stream()
                        .map(
                                row -> {
                                    var reasons = new ArrayList<>(page.blockers());
                                    if (!"ENABLED".equals(row.getStatus()))
                                        reasons.add(
                                                new AccessQueryViews.Blocker(
                                                        "ROLE_DISABLED",
                                                        row.getId().toString(),
                                                        row.getName()));
                                    return new AccessQueryViews.PageRole(
                                            role(row),
                                            pageEffective(page)
                                                    && "ENABLED".equals(row.getStatus()),
                                            row.getUserCount(),
                                            List.copyOf(reasons));
                                })
                        .toList();
        return result(
                rows,
                query,
                queries.pageRoleCount(mid, uid, query.keyword(), input.isIncludeInactive()),
                subject,
                page,
                inspection);
    }

    /**
     * One explicitly selected user's explanation shares the role-list transaction and timestamp.
     */
    private AccessQueryViews.PageUser inspect(
            long menuId,
            long userId,
            AccessQueryViews.PageInfo page,
            AccessQueryViews.UserIdentity subject) {
        var row = queries.pageUsers(menuId, userId, null, true, 0, 1).getFirst();
        boolean effective = effective(page, row.getActiveSourceCount());
        boolean moduleAccess =
                page.moduleRegistered()
                        && !queries.moduleUsers(page.moduleKey(), List.of(userId)).isEmpty();
        return new AccessQueryViews.PageUser(
                subject,
                effective,
                effective && subject.accountUsable(),
                moduleAccess,
                row.getSourceCount(),
                List.of(),
                blockers(page, subject, row.getSourceCount(), row.getActiveSourceCount()));
    }

    private Query validate(AccessQueryDto input) {
        long actor = current.requireNormal().getId();
        authorization.recheckCurrentRequest(actor);
        input.toPage();
        String term = input.getKeyword() == null ? "" : input.getKeyword().strip();
        if (term.codePointCount(0, term.length()) > 64
                || (input.getModuleKey() != null
                        && !input.getModuleKey().matches("[A-Za-z][A-Za-z0-9_:-]{0,63}"))) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        String keyword =
                term.isEmpty()
                        ? null
                        : term.replace("!", "!!").replace("%", "!%").replace("_", "!_");
        return new Query(
                input.getPage(),
                input.getSize(),
                (long) (input.getPage() - 1) * input.getSize(),
                term,
                keyword);
    }

    private Map<Long, AccessQueryViews.PageInfo> pageMap() {
        List<MenuEntity> all =
                menus.selectList(
                        new LambdaQueryWrapper<MenuEntity>()
                                .select(
                                        MenuEntity::getId,
                                        MenuEntity::getParentId,
                                        MenuEntity::getName,
                                        MenuEntity::getType,
                                        MenuEntity::getPath,
                                        MenuEntity::getModuleKey,
                                        MenuEntity::getEnabled,
                                        MenuEntity::getVisible)
                                .orderByAsc(MenuEntity::getId)
                                .last("LIMIT 1001"));
        if (all.size() > 1000) throw BusinessException.error(ErrorCode.CONFLICT);
        Map<Long, MenuEntity> byId =
                all.stream().collect(Collectors.toMap(MenuEntity::getId, Function.identity()));
        Map<Long, AccessQueryViews.PageInfo> result = new HashMap<>();
        for (MenuEntity page : all) {
            if (!"PAGE".equals(page.getType())) continue;
            List<AccessQueryViews.Blocker> reasons = new ArrayList<>();
            List<String> names = new ArrayList<>();
            Set<Long> visited = new HashSet<>();
            boolean visible = true;
            MenuEntity cursor = page;
            while (cursor != null) {
                if (!visited.add(cursor.getId())
                        || visited.size() > 5
                        || (cursor != page && !"DIRECTORY".equals(cursor.getType()))) {
                    reasons.add(
                            new AccessQueryViews.Blocker(
                                    "INVALID_MENU_TREE", page.getId().toString(), page.getName()));
                    break;
                }
                names.add(cursor.getName());
                visible &= Boolean.TRUE.equals(cursor.getVisible());
                if (!Boolean.TRUE.equals(cursor.getEnabled()))
                    reasons.add(
                            new AccessQueryViews.Blocker(
                                    cursor == page ? "PAGE_DISABLED" : "ANCESTOR_DISABLED",
                                    cursor.getId().toString(),
                                    cursor.getName()));
                if (cursor.getParentId() == null) break;
                MenuEntity parent = byId.get(cursor.getParentId());
                if (parent == null)
                    reasons.add(
                            new AccessQueryViews.Blocker(
                                    "INVALID_MENU_TREE", cursor.getParentId().toString(), "缺失目录"));
                cursor = parent;
            }
            boolean chainEnabled = reasons.isEmpty();
            boolean registered = modules.contains(page.getModuleKey());
            if (!registered)
                reasons.add(
                        new AccessQueryViews.Blocker(
                                "MODULE_UNREGISTERED",
                                page.getId().toString(),
                                page.getModuleKey()));
            Collections.reverse(names);
            result.put(
                    page.getId(),
                    new AccessQueryViews.PageInfo(
                            page.getId().toString(),
                            page.getName(),
                            page.getPath(),
                            String.join(" / ", names),
                            page.getModuleKey(),
                            Boolean.TRUE.equals(page.getEnabled()),
                            chainEnabled,
                            visible,
                            registered,
                            List.copyOf(reasons)));
        }
        return result;
    }

    private AccessQueryViews.PageInfo requirePage(long id) {
        var page = pageMap().get(id);
        if (page == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return page;
    }

    private AccessQueryViews.UserIdentity requireUser(long id) {
        var row = queries.user(id);
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return identities(List.of(row)).getFirst();
    }

    private AccessQueryRows.RoleRow requireRole(long id) {
        var role = queries.role(id);
        if (role == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return role;
    }

    private List<AccessQueryViews.UserIdentity> identities(
            List<? extends AccessQueryRows.UserRow> rows) {
        if (rows.isEmpty()) return List.of();
        Map<Long, List<com.streamfusion.platform.system.user.pojo.vo.DepartmentVo>> assigned =
                new HashMap<>();
        List<Long> ids = rows.stream().map(AccessQueryRows.UserRow::getId).distinct().toList();
        for (int start = 0; start < ids.size(); start += 500) {
            assigned.putAll(
                    departments.forUsers(ids.subList(start, Math.min(ids.size(), start + 500))));
        }
        return rows.stream().map(row -> user(row, assigned)).toList();
    }

    private Map<Long, AccessQueryViews.UserIdentity> identityMap(
            List<? extends AccessQueryRows.UserRow> rows) {
        Map<Long, AccessQueryViews.UserIdentity> result = new LinkedHashMap<>();
        for (var identity : identities(rows)) result.put(Long.parseLong(identity.id()), identity);
        return result;
    }

    private AccessQueryViews.UserIdentity user(
            AccessQueryRows.UserRow row,
            Map<Long, List<com.streamfusion.platform.system.user.pojo.vo.DepartmentVo>> assigned) {
        boolean mustChange =
                row.getStatus() == 0 || Boolean.TRUE.equals(row.getMustChangePassword());
        boolean loginRestricted =
                row.getLockedUntil() != null
                        && row.getLockedUntil()
                                .isAfter(
                                        LocalDateTime.ofInstant(
                                                clock.instant(), ZoneId.of("Asia/Shanghai")));
        return new AccessQueryViews.UserIdentity(
                row.getId().toString(),
                row.getUsername(),
                row.getNickname(),
                row.getStatus(),
                mustChange,
                loginRestricted,
                row.getStatus() == 1 && !mustChange,
                assigned.getOrDefault(row.getId(), List.of()).stream()
                        .map(
                                department ->
                                        new AccessQueryViews.DepartmentIdentity(
                                                department.getId(), department.getName()))
                        .toList());
    }

    private static AccessQueryViews.RoleIdentity role(AccessQueryRows.RoleRow row) {
        return new AccessQueryViews.RoleIdentity(
                row.getId().toString(), row.getCode(), row.getName(), row.getStatus());
    }

    private static Map<Long, List<AccessQueryViews.RoleIdentity>> sources(
            List<AccessQueryRows.SourceRow> rows) {
        return rows.stream()
                .collect(
                        Collectors.groupingBy(
                                AccessQueryRows.SourceRow::getSubjectId,
                                Collectors.mapping(AccessQueryService::role, Collectors.toList())));
    }

    private static boolean pageEffective(AccessQueryViews.PageInfo page) {
        return page != null && page.chainEnabled() && page.moduleRegistered();
    }

    private static boolean effective(AccessQueryViews.PageInfo page, long activeSources) {
        return activeSources > 0 && pageEffective(page);
    }

    private static List<AccessQueryViews.Blocker> blockers(
            AccessQueryViews.PageInfo page,
            AccessQueryViews.UserIdentity user,
            long sourceCount,
            long activeSourceCount) {
        List<AccessQueryViews.Blocker> result = new ArrayList<>(page.blockers());
        if (sourceCount == 0)
            result.add(new AccessQueryViews.Blocker("NO_PAGE_GRANT", page.id(), page.name()));
        else if (activeSourceCount == 0)
            result.add(new AccessQueryViews.Blocker("NO_ACTIVE_ROLE", user.id(), user.username()));
        if (user.status() == 2)
            result.add(new AccessQueryViews.Blocker("USER_DISABLED", user.id(), user.username()));
        if (user.mustChangePassword())
            result.add(
                    new AccessQueryViews.Blocker(
                            "PASSWORD_CHANGE_REQUIRED", user.id(), user.username()));
        return List.copyOf(result);
    }

    private static boolean matches(String value, String keyword) {
        return value != null
                && value.toLowerCase(java.util.Locale.ROOT)
                        .contains(keyword.toLowerCase(java.util.Locale.ROOT));
    }

    private static <T> List<T> slice(List<T> rows, Query query) {
        int start = (int) Math.min(rows.size(), query.offset());
        return rows.subList(start, Math.min(rows.size(), start + query.size()));
    }

    private <T> AccessQueryViews.Result<T> result(
            List<T> rows,
            Query query,
            long total,
            AccessQueryViews.UserIdentity user,
            AccessQueryViews.PageInfo page) {
        return result(rows, query, total, user, page, null);
    }

    private <T> AccessQueryViews.Result<T> result(
            List<T> rows,
            Query query,
            long total,
            AccessQueryViews.UserIdentity user,
            AccessQueryViews.PageInfo page,
            AccessQueryViews.PageUser inspection) {
        return new AccessQueryViews.Result<>(
                List.copyOf(rows),
                query.page(),
                query.size(),
                total,
                clock.instant(),
                user,
                page,
                inspection);
    }

    private record Query(int page, int size, long offset, String term, String keyword) {}
}
