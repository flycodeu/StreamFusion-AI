package com.streamfusion.platform.auth.service;

import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.pojo.vo.AuthUserVo;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.menu.service.MenuService;
import com.streamfusion.platform.user.converter.UserVoConverter;
import com.streamfusion.platform.user.pojo.dto.UserProfileUpdateDto;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.vo.UserProfileVo;
import com.streamfusion.platform.user.service.UserRules;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import java.util.Objects;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.TransactionTemplate;

@Service
public class ProfileService {
    private final UserService users;
    private final UserRules rules;
    private final UserVoConverter converter;
    private final CurrentUserService current;
    private final AccessService access;
    private final MenuService menus;
    private final AccessMapper accessMapper;
    private final PasswordService passwords;
    private final AuditService audit;
    private final Clock clock;
    private final TransactionTemplate transactions;

    public ProfileService(
            UserService users,
            UserRules rules,
            UserVoConverter converter,
            CurrentUserService current,
            AccessService access,
            MenuService menus,
            AccessMapper accessMapper,
            PasswordService passwords,
            AuditService audit,
            Clock clock,
            PlatformTransactionManager manager) {
        this.users = users;
        this.rules = rules;
        this.converter = converter;
        this.current = current;
        this.access = access;
        this.menus = menus;
        this.accessMapper = accessMapper;
        this.passwords = passwords;
        this.audit = audit;
        this.clock = clock;
        this.transactions = new TransactionTemplate(manager);
        this.transactions.setIsolationLevel(
                org.springframework.transaction.TransactionDefinition.ISOLATION_READ_COMMITTED);
    }

    @Transactional(readOnly = true)
    public AuthUserVo me() {
        UserEntity user = current.requireUser();
        if (CurrentUserService.requiresPasswordChange(user)) {
            return new AuthUserVo(converter.profile(user), List.of(), List.of(), List.of(), false);
        }
        var snapshot = access.snapshot(user.getId());
        return new AuthUserVo(
                converter.profile(user),
                snapshot.getModules(),
                snapshot.getRoles(),
                menus.routes(user.getId()),
                snapshot.isSuperAdmin());
    }

    @Transactional
    public UserProfileVo update(UserProfileUpdateDto input, AuditContextDto context) {
        SessionPrincipalDto principal = current.principal();
        UserEntity user = users.lockById(principal.getUserId());
        current.validate(principal, user);
        if (CurrentUserService.requiresPasswordChange(user))
            throw BusinessException.error(ErrorCode.PASSWORD_CHANGE_REQUIRED);
        if (users.updateProfile(
                        user.getId(),
                        rules.version(input.getVersion()),
                        rules.nickname(input.getNickname()),
                        rules.avatarKey(input.getAvatarKey()),
                        rules.phone(input.getPhone()),
                        rules.email(input.getEmail()),
                        rules.gender(input.getGender()),
                        user.getId(),
                        now())
                != 1) {
            throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        }
        audit.record(
                user.getId(), "USER", user.getId(), "PROFILE_UPDATE", "SUCCESS", null, context);
        return converter.profile(users.getById(user.getId()));
    }

    public void changePassword(String oldPassword, String newPassword, AuditContextDto context) {
        UserEntity observed = current.requireUser();
        passwords.validateReplacement(oldPassword, newPassword);
        if (!passwords.matches(oldPassword, observed.getPassword())) {
            throw BusinessException.error(ErrorCode.CURRENT_PASSWORD_INVALID);
        }
        String encoded = passwords.encode(newPassword);
        SessionPrincipalDto principal = current.principal();
        transactions.executeWithoutResult(
                status -> {
                    if (accessMapper.lockSuperAdminRole() == null)
                        throw BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
                    UserEntity user = users.lockById(principal.getUserId());
                    current.validate(principal, user);
                    if (!Objects.equals(user.getPassword(), observed.getPassword())
                            || users.changePassword(
                                            user.getId(),
                                            observed.getPassword(),
                                            observed.getSessionVersion(),
                                            encoded,
                                            now())
                                    != 1) {
                        throw BusinessException.error(ErrorCode.UNAUTHORIZED);
                    }
                    audit.record(
                            user.getId(),
                            "USER",
                            user.getId(),
                            "PASSWORD_CHANGE",
                            "SUCCESS",
                            null,
                            context);
                });
    }

    private LocalDateTime now() {
        return LocalDateTime.now(clock.withZone(ZoneId.of("Asia/Shanghai")));
    }
}
