package com.streamfusion.platform.user.service.impl;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.pojo.vo.RoleVo;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.auth.service.PasswordService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.department.service.DepartmentService;
import com.streamfusion.platform.loginrecord.service.LoginRecordService;
import com.streamfusion.platform.user.converter.UserVoConverter;
import com.streamfusion.platform.user.pojo.dto.UserCreateDto;
import com.streamfusion.platform.user.pojo.dto.UserManagementUpdateDto;
import com.streamfusion.platform.user.pojo.dto.UserQueryDto;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.pojo.vo.DepartmentVo;
import com.streamfusion.platform.user.pojo.vo.UserSummaryVo;
import com.streamfusion.platform.user.pojo.vo.UserVo;
import com.streamfusion.platform.user.service.UserManagementService;
import com.streamfusion.platform.user.service.UserRules;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.dao.DuplicateKeyException;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

/** 按用户管理权限处理普通账号，协调事务、审计和返回对象。 */
@Service
@RequiredArgsConstructor
public class UserManagementServiceImpl implements UserManagementService {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");

    private final UserService users;
    private final UserRules rules;
    private final CurrentUserService currentUser;
    private final ModuleAuthorizationService moduleAuthorization;
    private final PasswordService passwords;
    private final AccessMapper accessMapper;
    private final AccessService access;
    private final AuditService audit;
    private final Clock clock;
    private final UserVoConverter converter;
    private final DepartmentService departments;
    private final LoginRecordService loginRecords;

    @Override
    @Transactional(readOnly = true)
    public PageResultVo<UserSummaryVo> page(UserQueryDto query) {
        if (query == null) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        query.validateFilters();
        String keyword = query.getKeyword();
        String search = keyword == null || keyword.isBlank() ? null : keyword.strip();
        var filter =
                new LambdaQueryWrapper<UserEntity>()
                        .select(
                                UserEntity::getId,
                                UserEntity::getUsername,
                                UserEntity::getNickname,
                                UserEntity::getAvatarKey,
                                UserEntity::getStatus,
                                UserEntity::getLockedUntil,
                                UserEntity::getVersion);
        if (search != null) {
            search = search.replace("!", "!!").replace("%", "!%").replace("_", "!_");
            filter.apply(
                    "(LOWER(username) LIKE CONCAT('%', LOWER({0}), '%') ESCAPE '!'"
                            + " OR LOWER(nickname) LIKE CONCAT('%', LOWER({0}), '%') ESCAPE '!')",
                    search);
        }
        if (query.getStatus() != null) {
            filter.eq(UserEntity::getStatus, query.getStatus());
        }
        filter.orderByDesc(UserEntity::getCreatedAt).orderByDesc(UserEntity::getId);
        var page = users.page(query.<UserEntity>toPage(), filter);
        return PageResultVo.from(page, summaries(page.getRecords()));
    }

    @Override
    @Transactional(readOnly = true)
    public UserVo get(String id) {
        return view(find(rules.id(id)));
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public UserVo create(UserCreateDto input, AuditContextDto context) {
        // The controller module interceptor has already checked access before this workflow.
        String username = rules.username(input.getUsername());
        String nickname = rules.nickname(input.getNickname());
        String avatarKey = rules.avatarKey(input.getAvatarKey());
        String phone = rules.phone(input.getPhone());
        String email = rules.email(input.getEmail());
        int gender = rules.gender(input.getGender());
        String temporaryPassword = passwords.initialPassword();
        String hash = passwords.encode(temporaryPassword);
        UserEntity actor = lockAndRequireActor();
        LocalDateTime now = now();
        UserEntity user = new UserEntity();
        user.setUsername(username);
        user.setPassword(hash);
        user.setNickname(nickname);
        user.setAvatarKey(avatarKey);
        user.setPhone(phone);
        user.setEmail(email);
        user.setGender(gender);
        user.setStatus(UserStatus.PENDING_PASSWORD.getCode());
        user.setMustChangePassword(true);
        user.setSessionVersion(0L);
        user.setFailedLoginCount(0);
        user.setLockedUntil(null);
        user.setVersion(0L);
        user.setCreatedAt(now);
        user.setUpdatedAt(now);
        user.setCreatedBy(actor.getId());
        user.setUpdatedBy(actor.getId());
        try {
            if (!users.save(user)) throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
        } catch (DuplicateKeyException exception) {
            throw BusinessException.error(ErrorCode.USERNAME_TAKEN);
        }
        if (input.getDepartmentIds() != null) {
            departments.assignForUserWrite(user.getId(), input.getDepartmentIds(), context);
        }
        audit.record(
                actor.getId(),
                "USER",
                user.getId(),
                "USER_CREATE",
                "SUCCESS",
                null,
                context,
                userSummary(user));
        UserVo result = view(user);
        result.setTemporaryPassword(temporaryPassword);
        return result;
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public UserVo update(String id, UserManagementUpdateDto input, AuditContextDto context) {
        long userId = rules.id(id);
        long version = rules.version(input.getVersion());
        String nickname = rules.nickname(input.getNickname());
        String avatarKey = rules.avatarKey(input.getAvatarKey());
        String phone = rules.phone(input.getPhone());
        String email = rules.email(input.getEmail());
        int gender = rules.gender(input.getGender());
        UserEntity actor = lockAndRequireActor();
        UserEntity target = lockOrdinaryTarget(userId, actor);
        requireVersion(target, version, ErrorCode.VERSION_CONFLICT);
        if (users.updateProfile(
                        userId,
                        version,
                        nickname,
                        avatarKey,
                        phone,
                        email,
                        gender,
                        actor.getId(),
                        now())
                != 1) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        if (input.getDepartmentIds() != null) {
            departments.assignForUserWrite(userId, input.getDepartmentIds(), context);
        }
        UserEntity updated = find(userId);
        audit.record(
                actor.getId(),
                "USER",
                userId,
                "USER_UPDATE",
                "SUCCESS",
                null,
                context,
                userSummary(updated));
        return view(updated);
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public UserVo changeStatus(
            String id, String requestedVersion, boolean enabled, AuditContextDto context) {
        long userId = rules.id(id);
        long version = rules.version(requestedVersion);
        UserEntity actor = lockAndRequireActor();
        UserEntity target = lockOrdinaryTarget(userId, actor);
        requireVersion(target, version, ErrorCode.VERSION_CONFLICT);
        // Enabling an already usable account preserves its pending-password state.
        int status =
                enabled
                        ? (Boolean.TRUE.equals(target.getMustChangePassword())
                                ? UserStatus.PENDING_PASSWORD.getCode()
                                : UserStatus.NORMAL.getCode())
                        : UserStatus.BANNED.getCode();
        if ((enabled && target.getStatus() != UserStatus.BANNED.getCode())
                || target.getStatus() == status) {
            return view(target);
        }
        if (users.changeStatus(userId, version, status, actor.getId(), now()) != 1) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        if (!enabled) loginRecords.endAll(userId, "ACCOUNT_DISABLED");
        audit.record(
                actor.getId(),
                "USER",
                userId,
                enabled ? "USER_ENABLE" : "USER_DISABLE",
                "SUCCESS",
                null,
                context,
                userSummary(target));
        return view(find(userId));
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public UserVo resetPassword(String id, String requestedVersion, AuditContextDto context) {
        long userId = rules.id(id);
        long version = rules.version(requestedVersion);
        String temporaryPassword = passwords.initialPassword();
        String hash = passwords.encode(temporaryPassword);
        UserEntity actor = lockAndRequireActor();
        UserEntity target = lockOrdinaryTarget(userId, actor);
        requireVersion(target, version, ErrorCode.VERSION_CONFLICT);
        if (users.resetPassword(userId, version, hash, actor.getId(), now()) != 1) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        loginRecords.endAll(userId, "PASSWORD_RESET");
        audit.record(
                actor.getId(),
                "USER",
                userId,
                "USER_RESET_PASSWORD",
                "SUCCESS",
                null,
                context,
                userSummary(target));
        UserVo result = view(find(userId));
        result.setTemporaryPassword(temporaryPassword);
        return result;
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public UserVo forceLogout(String id, String requestedVersion, AuditContextDto context) {
        long userId = rules.id(id);
        long version = rules.version(requestedVersion);
        UserEntity actor = lockAndRequireActor();
        UserEntity target = lockOrdinaryTarget(userId, actor);
        requireVersion(target, version, ErrorCode.VERSION_CONFLICT);
        if (users.forceLogout(userId, version, actor.getId(), now()) != 1) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        loginRecords.endAll(userId, "FORCED_LOGOUT");
        audit.record(
                actor.getId(),
                "USER",
                userId,
                "USER_FORCE_LOGOUT",
                "SUCCESS",
                null,
                context,
                userSummary(target));
        return view(find(userId));
    }

    @Override
    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void delete(String id, String requestedVersion, AuditContextDto context) {
        long userId = rules.id(id);
        long version = rules.version(requestedVersion);
        UserEntity actor = lockAndRequireActor();
        UserEntity target = lockOrdinaryTarget(userId, actor);
        requireVersion(target, version, ErrorCode.PRECONDITION_FAILED);
        Map<String, Object> summary = userSummary(target);
        users.deleteRoleBindings(userId);
        departments.removeUserBindings(userId);
        if (users.hardDelete(userId, version) != 1) {
            throw BusinessException.error(ErrorCode.PRECONDITION_FAILED);
        }
        loginRecords.endAll(userId, "ACCOUNT_DELETED");
        audit.record(
                actor.getId(), "USER", userId, "USER_DELETE", "SUCCESS", null, context, summary);
    }

    private UserEntity lockAndRequireActor() {
        if (accessMapper.lockSuperAdminRole() == null) {
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        }
        UserEntity actor = currentUser.requireNormal();
        moduleAuthorization.recheckCurrentRequest(actor.getId());
        return actor;
    }

    private UserEntity lockOrdinaryTarget(long userId, UserEntity actor) {
        UserEntity target = users.lockById(userId);
        if (target == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (Objects.equals(actor.getId(), target.getId()) || access.isSuperAdmin(userId)) {
            throw BusinessException.error(ErrorCode.PROTECTED_ACCOUNT);
        }
        return target;
    }

    private UserEntity find(long userId) {
        UserEntity user = users.getById(userId);
        if (user == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return user;
    }

    private UserVo view(UserEntity user) {
        List<DepartmentVo> assigned =
                departments.forUsers(List.of(user.getId())).getOrDefault(user.getId(), List.of());
        return converter.detail(user, accessMapper.findRoles(user.getId()), assigned);
    }

    private List<UserSummaryVo> summaries(List<UserEntity> page) {
        if (page.isEmpty()) return List.of();
        List<Long> userIds = page.stream().map(UserEntity::getId).toList();
        Map<Long, List<RoleVo>> roles = new HashMap<>();
        for (var assignment : accessMapper.findRolesForUsers(userIds)) {
            roles.computeIfAbsent(assignment.getUserId(), ignored -> new ArrayList<>())
                    .add(
                            new RoleVo(
                                    assignment.getId(),
                                    assignment.getCode(),
                                    assignment.getName()));
        }
        Map<Long, List<DepartmentVo>> assignedDepartments = departments.forUsers(userIds);
        return page.stream()
                .map(
                        user ->
                                converter.summary(
                                        user,
                                        roles.getOrDefault(user.getId(), List.of()),
                                        assignedDepartments.getOrDefault(user.getId(), List.of())))
                .toList();
    }

    private static void requireVersion(UserEntity target, long expected, ErrorCode error) {
        if (target.getVersion() != expected) throw BusinessException.error(error);
    }

    private static Map<String, Object> userSummary(UserEntity user) {
        Map<String, Object> summary = new HashMap<>();
        summary.put("username", user.getUsername());
        summary.put("nickname", user.getNickname());
        return summary;
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), BUSINESS_ZONE);
    }
}
