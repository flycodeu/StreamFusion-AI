package com.streamfusion.platform.auth.service;

import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.service.UserRules;
import com.streamfusion.platform.user.service.UserService;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.Objects;
import java.util.function.Consumer;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

@Service
public class AuthenticationService {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final UserService users;
    private final UserRules rules;
    private final PasswordService passwords;
    private final AuditService audit;
    private final Clock clock;
    private final TransactionTemplate transactions;

    public AuthenticationService(
            UserService users,
            UserRules rules,
            PasswordService passwords,
            AuditService audit,
            Clock clock,
            PlatformTransactionManager manager) {
        this.users = users;
        this.rules = rules;
        this.passwords = passwords;
        this.audit = audit;
        this.clock = clock;
        this.transactions = new TransactionTemplate(manager);
    }

    public SessionPrincipalDto authenticate(
            String username, String password, AuditContextDto context) {
        return authenticate(username, password, context, principal -> {});
    }

    /** Completes successful session establishment under the same account lock as credentials. */
    public SessionPrincipalDto authenticate(
            String username,
            String password,
            AuditContextDto context,
            Consumer<SessionPrincipalDto> establishSession) {
        rules.username(username);
        if (password == null || password.isEmpty() || password.length() > 4096) {
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        }
        UserEntity observed = users.findByUsername(username);
        LocalDateTime now = LocalDateTime.now(clock.withZone(ZONE));
        if (observed == null) {
            passwords.verifyDummy(password);
            transactions.executeWithoutResult(
                    status ->
                            audit.record(
                                    null,
                                    "USER",
                                    null,
                                    "LOGIN",
                                    "FAILURE",
                                    "INVALID_CREDENTIALS",
                                    context));
            throw BusinessException.error(ErrorCode.LOGIN_FAILED);
        }
        boolean eligible =
                observed.getStatus() != UserStatus.BANNED.getCode()
                        && (observed.getLockedUntil() == null
                                || !now.isBefore(observed.getLockedUntil()));
        boolean matches = eligible && passwords.matches(password, observed.getPassword());
        SessionPrincipalDto result =
                transactions.execute(
                        status -> {
                            UserEntity current = users.lockById(observed.getId());
                            LocalDateTime checkedAt = LocalDateTime.now(clock.withZone(ZONE));
                            if (current == null
                                    || current.getStatus() == UserStatus.BANNED.getCode()
                                    || !Objects.equals(
                                            current.getPassword(), observed.getPassword())) {
                                audit.record(
                                        null,
                                        "USER",
                                        observed.getId(),
                                        "LOGIN",
                                        "FAILURE",
                                        "ACCOUNT_UNAVAILABLE",
                                        context);
                                return null;
                            }
                            if (current.getLockedUntil() != null
                                    && checkedAt.isBefore(current.getLockedUntil())) {
                                audit.record(
                                        null,
                                        "USER",
                                        current.getId(),
                                        "LOGIN",
                                        "DENIED",
                                        "ACCOUNT_COOLING_DOWN",
                                        context);
                                return null;
                            }
                            // A request that began during cooldown must not treat an untested
                            // password as a failure.
                            if (!eligible) {
                                audit.record(
                                        null,
                                        "USER",
                                        current.getId(),
                                        "LOGIN",
                                        "DENIED",
                                        "RETRY_REQUIRED",
                                        context);
                                return null;
                            }
                            int failures =
                                    current.getLockedUntil() == null
                                            ? current.getFailedLoginCount()
                                            : 0;
                            if (!matches) {
                                failures = Math.min(failures + 1, 5);
                                users.updateLoginState(
                                        current.getId(),
                                        current.getPassword(),
                                        current.getSessionVersion(),
                                        failures,
                                        failures == 5 ? checkedAt.plusMinutes(15) : null,
                                        checkedAt);
                                audit.record(
                                        null,
                                        "USER",
                                        current.getId(),
                                        "LOGIN",
                                        "FAILURE",
                                        "INVALID_CREDENTIALS",
                                        context);
                                return null;
                            }
                            users.updateLoginState(
                                    current.getId(),
                                    current.getPassword(),
                                    current.getSessionVersion(),
                                    0,
                                    null,
                                    checkedAt);
                            var principal =
                                    new SessionPrincipalDto(
                                            current.getId(),
                                            current.getSessionVersion(),
                                            clock.instant());
                            establishSession.accept(principal);
                            return principal;
                        });
        // Throw after commit so failure counters and audit survive a rejected login.
        if (result == null) throw BusinessException.error(ErrorCode.LOGIN_FAILED);
        return result;
    }

    public void recordLogin(SessionPrincipalDto principal, AuditContextDto context) {
        transactions.executeWithoutResult(
                status ->
                        audit.record(
                                principal.getUserId(),
                                "USER",
                                principal.getUserId(),
                                "LOGIN",
                                "SUCCESS",
                                null,
                                context));
    }

    public void recordLogout(SessionPrincipalDto principal, AuditContextDto context) {
        transactions.executeWithoutResult(
                status ->
                        audit.record(
                                principal.getUserId(),
                                "USER",
                                principal.getUserId(),
                                "LOGOUT",
                                "SUCCESS",
                                null,
                                context));
    }
}
