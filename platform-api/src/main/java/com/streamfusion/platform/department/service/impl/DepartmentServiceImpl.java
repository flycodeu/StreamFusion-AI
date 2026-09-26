package com.streamfusion.platform.department.service.impl;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.department.mapper.DepartmentMapper;
import com.streamfusion.platform.department.pojo.dto.DepartmentTreeQueryDto;
import com.streamfusion.platform.department.pojo.dto.DepartmentUpdateDto;
import com.streamfusion.platform.department.pojo.dto.DepartmentWriteDto;
import com.streamfusion.platform.department.pojo.dto.UserDepartmentsUpdateDto;
import com.streamfusion.platform.department.pojo.entity.DepartmentEntity;
import com.streamfusion.platform.department.pojo.vo.DepartmentNodeVo;
import com.streamfusion.platform.department.pojo.vo.DepartmentOptionVo;
import com.streamfusion.platform.department.pojo.vo.UserDepartmentsVo;
import com.streamfusion.platform.department.service.DepartmentRules;
import com.streamfusion.platform.department.service.DepartmentService;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.vo.DepartmentVo;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.dao.DataIntegrityViolationException;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class DepartmentServiceImpl implements DepartmentService {
    private static final int MAX_NODES = 1000;
    private static final int MAX_DEPTH = 8;
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

    private final DepartmentMapper departments;
    private final DepartmentRules rules;
    private final UserService users;
    private final AccessMapper accessMapper;
    private final AccessService access;
    private final CurrentUserService currentUser;
    private final ModuleAuthorizationService authorization;
    private final AuditService audit;
    private final Clock clock;

    @Override
    @Transactional(readOnly = true)
    public List<DepartmentNodeVo> tree(DepartmentTreeQueryDto query) {
        String name = query == null || query.getName() == null ? null : query.getName().strip();
        if (name != null && name.codePointCount(0, name.length()) > 64) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        if (name != null && name.isEmpty()) name = null;
        List<DepartmentEntity> all = load();
        Map<Long, List<DepartmentEntity>> children = children(all);
        Map<Long, Long> counts = counts();
        List<DepartmentNodeVo> roots = new ArrayList<>();
        for (DepartmentEntity root : children.getOrDefault(null, List.of())) {
            DepartmentNodeVo node = filtered(root, children, counts, name);
            if (node != null) roots.add(node);
        }
        return List.copyOf(roots);
    }

    @Override
    @Transactional(readOnly = true)
    public List<DepartmentOptionVo> options() {
        return load().stream()
                .map(
                        dept ->
                                new DepartmentOptionVo(
                                        dept.getId().toString(),
                                        string(dept.getParentId()),
                                        dept.getName()))
                .toList();
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public DepartmentNodeVo create(DepartmentWriteDto input, AuditContextDto context) {
        DepartmentRules.Values values = rules.write(input);
        long actorId = lockActor();
        List<DepartmentEntity> all = load();
        if (all.size() >= MAX_NODES) throw BusinessException.error(ErrorCode.CONFLICT);
        Map<Long, DepartmentEntity> byId = byId(all);
        validatePlacement(values.parentId(), null, byId, children(all));
        uniqueSibling(values, null, all);
        DepartmentEntity dept = new DepartmentEntity();
        dept.setParentId(values.parentId());
        dept.setName(values.name());
        dept.setSortOrder(values.sortOrder());
        dept.setVersion(0L);
        dept.setCreatedAt(now());
        dept.setUpdatedAt(dept.getCreatedAt());
        dept.setCreatedBy(actorId);
        dept.setUpdatedBy(actorId);
        try {
            if (departments.insert(dept) != 1) {
                throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
            }
        } catch (DataIntegrityViolationException ex) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        audit.record(actorId, "DEPT", dept.getId(), "DEPT_CREATE", "SUCCESS", null, context);
        return node(dept, 0, List.of());
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public DepartmentNodeVo update(String id, DepartmentUpdateDto input, AuditContextDto context) {
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        long deptId = rules.id(id);
        long version = rules.version(input.getVersion());
        DepartmentRules.Values values = rules.write(input);
        long actorId = lockActor();
        List<DepartmentEntity> all = load();
        Map<Long, DepartmentEntity> byId = byId(all);
        DepartmentEntity current = byId.get(deptId);
        if (current == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (!current.getVersion().equals(version) || version == Long.MAX_VALUE) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        validatePlacement(values.parentId(), deptId, byId, children(all));
        uniqueSibling(values, deptId, all);
        try {
            if (departments.update(
                            null,
                            new LambdaUpdateWrapper<DepartmentEntity>()
                                    .eq(DepartmentEntity::getId, deptId)
                                    .eq(DepartmentEntity::getVersion, version)
                                    .set(DepartmentEntity::getParentId, values.parentId())
                                    .set(DepartmentEntity::getName, values.name())
                                    .set(DepartmentEntity::getSortOrder, values.sortOrder())
                                    .setIncrBy(DepartmentEntity::getVersion, 1)
                                    .set(DepartmentEntity::getUpdatedAt, now())
                                    .set(DepartmentEntity::getUpdatedBy, actorId))
                    != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        } catch (DataIntegrityViolationException ex) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        audit.record(actorId, "DEPT", deptId, "DEPT_UPDATE", "SUCCESS", null, context);
        DepartmentEntity updated = departments.selectById(deptId);
        return node(updated, counts().getOrDefault(deptId, 0L), List.of());
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void delete(String id, String requestedVersion, AuditContextDto context) {
        long deptId = rules.id(id);
        long version = rules.version(requestedVersion);
        long actorId = lockActor();
        List<DepartmentEntity> all = load();
        DepartmentEntity target = byId(all).get(deptId);
        if (target == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (!target.getVersion().equals(version)) {
            throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        }
        if (!children(all).getOrDefault(deptId, List.of()).isEmpty()
                || departments.userCount(deptId) > 0) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        try {
            if (departments.delete(
                            new LambdaQueryWrapper<DepartmentEntity>()
                                    .eq(DepartmentEntity::getId, deptId)
                                    .eq(DepartmentEntity::getVersion, version))
                    != 1) throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        } catch (DataIntegrityViolationException ex) {
            throw BusinessException.error(ErrorCode.CONFLICT);
        }
        Map<String, Object> deletedSnapshot = new HashMap<>();
        deletedSnapshot.put("name", target.getName());
        deletedSnapshot.put("parentId", string(target.getParentId()));
        audit.record(
                actorId, "DEPT", deptId, "DEPT_DELETE", "SUCCESS", null, context, deletedSnapshot);
    }

    @Override
    @Transactional(readOnly = true)
    public UserDepartmentsVo userDepartments(String userId) {
        long id = rules.id(userId);
        UserEntity user = users.getById(id);
        if (user == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return new UserDepartmentsVo(
                userId,
                user.getVersion().toString(),
                forUsers(List.of(id)).getOrDefault(id, List.of()));
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public UserDepartmentsVo assign(
            String userId, UserDepartmentsUpdateDto input, AuditContextDto context) {
        if (input == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        long id = rules.id(userId);
        long version = rules.version(input.getVersion());
        List<Long> selected = rules.departmentIds(input.getDepartmentIds());
        long actorId = lockActor();
        UserEntity target = users.lockById(id);
        if (target == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        boolean superAdmin = access.isSuperAdmin(actorId);
        if (!superAdmin && (actorId == id || access.isSuperAdmin(id))) {
            throw BusinessException.error(ErrorCode.PROTECTED_ACCOUNT);
        }
        if (!target.getVersion().equals(version) || version == Long.MAX_VALUE) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        Map<Long, DepartmentEntity> available = byId(load());
        if (!available.keySet().containsAll(selected)) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        Set<Long> previous = new HashSet<>();
        for (DepartmentVo dept : forUsers(List.of(id)).getOrDefault(id, List.of())) {
            previous.add(Long.parseLong(dept.getId()));
        }
        if (previous.equals(new HashSet<>(selected))) return userDepartments(userId);
        departments.deleteUserBindings(id);
        LocalDateTime now = now();
        for (Long deptId : selected) {
            departments.insertUserBinding(id, deptId, now, actorId);
        }
        if (!users.update(
                null,
                new LambdaUpdateWrapper<UserEntity>()
                        .eq(UserEntity::getId, id)
                        .eq(UserEntity::getVersion, version)
                        .setIncrBy(UserEntity::getVersion, 1)
                        .set(UserEntity::getUpdatedAt, now)
                        .set(UserEntity::getUpdatedBy, actorId))) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        audit.record(
                actorId,
                "USER",
                id,
                "USER_DEPARTMENTS_UPDATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "beforeDepartmentIds",
                                previous.stream().sorted().map(String::valueOf).toList(),
                        "afterDepartmentIds", selected.stream().map(String::valueOf).toList()));
        return userDepartments(userId);
    }

    @Override
    @Transactional(readOnly = true)
    public Map<Long, List<DepartmentVo>> forUsers(List<Long> userIds) {
        Map<Long, List<DepartmentVo>> result = new HashMap<>();
        for (var row : departments.userDepartments(userIds)) {
            result.computeIfAbsent(row.userId(), ignored -> new ArrayList<>())
                    .add(new DepartmentVo(row.id(), row.name()));
        }
        result.replaceAll((ignored, values) -> List.copyOf(values));
        return result;
    }

    @Override
    public void removeUserBindings(long userId) {
        departments.deleteUserBindings(userId);
    }

    private long lockActor() {
        if (accessMapper.lockSuperAdminRole() == null) {
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        }
        long actorId = currentUser.requireNormal().getId();
        authorization.recheckCurrentRequest(actorId);
        return actorId;
    }

    private List<DepartmentEntity> load() {
        List<DepartmentEntity> all =
                departments.selectList(
                        new LambdaQueryWrapper<DepartmentEntity>()
                                .select(
                                        DepartmentEntity::getId,
                                        DepartmentEntity::getParentId,
                                        DepartmentEntity::getName,
                                        DepartmentEntity::getSortOrder,
                                        DepartmentEntity::getVersion)
                                .orderByAsc(DepartmentEntity::getSortOrder)
                                .orderByAsc(DepartmentEntity::getId)
                                .last("LIMIT 1001"));
        if (all.size() > MAX_NODES) throw BusinessException.error(ErrorCode.CONFLICT);
        Map<Long, DepartmentEntity> byId = byId(all);
        for (DepartmentEntity dept : all) depth(dept.getId(), byId, new HashSet<>());
        return all;
    }

    private static void validatePlacement(
            Long parentId,
            Long movingId,
            Map<Long, DepartmentEntity> byId,
            Map<Long, List<DepartmentEntity>> children) {
        if (parentId != null && !byId.containsKey(parentId)) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        for (Long current = parentId; current != null; current = byId.get(current).getParentId()) {
            if (current.equals(movingId)) throw BusinessException.error(ErrorCode.CONFLICT);
        }
        int top = parentId == null ? 1 : depth(parentId, byId, new HashSet<>()) + 1;
        int height = movingId == null ? 1 : height(movingId, children);
        if (top + height - 1 > MAX_DEPTH) throw BusinessException.error(ErrorCode.CONFLICT);
    }

    private static void uniqueSibling(
            DepartmentRules.Values values, Long currentId, List<DepartmentEntity> all) {
        String normalized = values.name().toLowerCase(Locale.ROOT);
        for (DepartmentEntity dept : all) {
            if (!dept.getId().equals(currentId)
                    && java.util.Objects.equals(dept.getParentId(), values.parentId())
                    && dept.getName().toLowerCase(Locale.ROOT).equals(normalized)) {
                throw BusinessException.error(ErrorCode.CONFLICT);
            }
        }
    }

    private static int depth(long id, Map<Long, DepartmentEntity> byId, Set<Long> seen) {
        if (!seen.add(id)) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        DepartmentEntity node = byId.get(id);
        if (node == null) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        int result = node.getParentId() == null ? 1 : 1 + depth(node.getParentId(), byId, seen);
        if (result > MAX_DEPTH) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        return result;
    }

    private static int height(long id, Map<Long, List<DepartmentEntity>> children) {
        int result = 1;
        for (DepartmentEntity child : children.getOrDefault(id, List.of())) {
            result = Math.max(result, 1 + height(child.getId(), children));
        }
        return result;
    }

    private Map<Long, Long> counts() {
        Map<Long, Long> result = new HashMap<>();
        for (var count : departments.memberCounts())
            result.put(count.deptId(), count.memberCount());
        return result;
    }

    private static Map<Long, DepartmentEntity> byId(List<DepartmentEntity> all) {
        Map<Long, DepartmentEntity> result = new HashMap<>();
        for (DepartmentEntity dept : all) result.put(dept.getId(), dept);
        return result;
    }

    private static Map<Long, List<DepartmentEntity>> children(List<DepartmentEntity> all) {
        Map<Long, List<DepartmentEntity>> result = new HashMap<>();
        for (DepartmentEntity dept : all) {
            result.computeIfAbsent(dept.getParentId(), ignored -> new ArrayList<>()).add(dept);
        }
        return result;
    }

    private static DepartmentNodeVo filtered(
            DepartmentEntity dept,
            Map<Long, List<DepartmentEntity>> children,
            Map<Long, Long> counts,
            String name) {
        List<DepartmentNodeVo> descendants = new ArrayList<>();
        for (DepartmentEntity child : children.getOrDefault(dept.getId(), List.of())) {
            DepartmentNodeVo matched = filtered(child, children, counts, name);
            if (matched != null) descendants.add(matched);
        }
        if (name != null && !dept.getName().contains(name) && descendants.isEmpty()) return null;
        return node(dept, counts.getOrDefault(dept.getId(), 0L), descendants);
    }

    private static DepartmentNodeVo node(
            DepartmentEntity dept, long memberCount, List<DepartmentNodeVo> children) {
        return new DepartmentNodeVo(
                dept.getId().toString(),
                string(dept.getParentId()),
                dept.getName(),
                dept.getSortOrder(),
                memberCount,
                dept.getVersion().toString(),
                List.copyOf(children));
    }

    private static String string(Long id) {
        return id == null ? null : id.toString();
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), BUSINESS_ZONE);
    }
}
