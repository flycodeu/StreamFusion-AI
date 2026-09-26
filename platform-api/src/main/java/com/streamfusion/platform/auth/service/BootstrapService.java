package com.streamfusion.platform.auth.service;

import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.department.mapper.DepartmentMapper;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.enums.UserGender;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.service.UserRules;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import java.util.Map;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

@Service
public class BootstrapService {
    private final UserService users;
    private final AccessMapper access;
    private final AccessService authorization;
    private final DepartmentMapper departments;
    private final UserRules rules;
    private final PasswordService passwords;
    private final AuditService audit;
    private final Clock clock;
    private final TransactionTemplate transactions;

    public BootstrapService(
            UserService users,
            AccessMapper access,
            AccessService authorization,
            DepartmentMapper departments,
            UserRules rules,
            PasswordService passwords,
            AuditService audit,
            Clock clock,
            PlatformTransactionManager manager) {
        this.users = users;
        this.access = access;
        this.authorization = authorization;
        this.departments = departments;
        this.rules = rules;
        this.passwords = passwords;
        this.audit = audit;
        this.clock = clock;
        this.transactions = new TransactionTemplate(manager);
    }

    public long initialize(String username, String nickname, String password, Long departmentId) {
        String account = rules.username(username);
        String displayName = rules.nickname(nickname);
        passwords.validateReplacement(null, password);
        return createAdministrator(account, displayName, password, false, departmentId);
    }

    /** 显式初始化默认管理员；初始凭据仅供首次登录，登录后必须按正常规则改密。 */
    public long initializeDefaultAdmin(
            String username, String nickname, String initialPassword, Long departmentId) {
        String account = rules.username(username);
        String displayName = rules.nickname(nickname);
        validateInitialCredential(initialPassword);
        return createAdministrator(account, displayName, initialPassword, true, departmentId);
    }

    private long createAdministrator(
            String account,
            String displayName,
            String password,
            boolean mustChangePassword,
            Long departmentId) {
        String hash = passwords.encode(password);
        return transactions.execute(
                status -> {
                    Long roleId = access.lockSuperAdminRole();
                    if (roleId == null || users.count() != 0 || audit.hasSuccessfulBootstrap()) {
                        throw BusinessException.error(ErrorCode.BOOTSTRAP_UNAVAILABLE);
                    }
                    if (departmentId != null
                            && (departmentId <= 0
                                    || departments.selectById(departmentId) == null)) {
                        throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
                    }
                    LocalDateTime now =
                            LocalDateTime.now(clock.withZone(ZoneId.of("Asia/Shanghai")));
                    UserEntity user = new UserEntity();
                    user.setUsername(account);
                    user.setNickname(displayName);
                    user.setPassword(hash);
                    user.setGender(UserGender.UNKNOWN.getCode());
                    user.setStatus(
                            mustChangePassword
                                    ? UserStatus.PENDING_PASSWORD.getCode()
                                    : UserStatus.NORMAL.getCode());
                    user.setMustChangePassword(mustChangePassword);
                    user.setSessionVersion(0L);
                    user.setFailedLoginCount(0);
                    user.setVersion(0L);
                    user.setCreatedAt(now);
                    user.setUpdatedAt(now);
                    if (!users.save(user)) {
                        throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
                    }
                    if (access.bindRole(user.getId(), roleId, now) != 1) {
                        throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
                    }
                    authorization.bindAllPages(roleId, now);
                    if (departmentId != null
                            && departments.insertUserBinding(
                                            user.getId(), departmentId, now, user.getId())
                                    != 1) {
                        throw BusinessException.error(ErrorCode.INTERNAL_ERROR);
                    }
                    audit.record(
                            user.getId(),
                            "USER",
                            user.getId(),
                            "BOOTSTRAP",
                            "SUCCESS",
                            null,
                            null,
                            Map.of(
                                    "username",
                                    account,
                                    "departmentIds",
                                    departmentId == null ? List.of() : List.of(departmentId)));
                    return user.getId();
                });
    }

    private static void validateInitialCredential(String password) {
        if (password == null || password.length() > 128) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        int length = password.codePointCount(0, password.length());
        if (length < 8
                || length > 64
                || password.codePoints()
                        .anyMatch(
                                cp ->
                                        Character.isISOControl(cp)
                                                || Character.isWhitespace(cp)
                                                || Character.isSpaceChar(cp))) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
    }
}
