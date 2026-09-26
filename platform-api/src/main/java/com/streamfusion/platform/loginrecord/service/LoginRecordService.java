package com.streamfusion.platform.loginrecord.service;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.auth.guard.ClientIpResolver;
import com.streamfusion.platform.auth.pojo.dto.SessionPrincipalDto;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.loginrecord.config.LoginRecordProperties;
import com.streamfusion.platform.loginrecord.mapper.LoginRecordMapper;
import com.streamfusion.platform.loginrecord.pojo.entity.LoginRecordEntity;
import com.streamfusion.platform.loginrecord.pojo.vo.LoginRecordVo;
import com.streamfusion.platform.user.pojo.entity.UserEntity;
import com.streamfusion.platform.user.pojo.enums.UserStatus;
import com.streamfusion.platform.user.service.UserRules;
import com.streamfusion.platform.user.service.UserService;
import jakarta.servlet.http.HttpServletRequest;
import java.io.Serializable;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class LoginRecordService {
    public static final String SESSION_ATTRIBUTE = "streamfusion.loginRecord";
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final LoginRecordMapper mapper;
    private final UserService users;
    private final UserRules rules;
    private final CurrentUserService current;
    private final AccessService access;
    private final ClientIpResolver clientIp;
    private final LoginClientClassifier classifier;
    private final AuthProperties auth;
    private final LoginRecordProperties properties;
    private final Clock clock;

    @Transactional
    public void start(SessionPrincipalDto principal, HttpServletRequest request) {
        UserEntity user = users.lockById(principal.getUserId());
        current.validate(principal, user);
        String ip = clientIp.resolve(request);
        var region = classifier.region(ip);
        var client = classifier.client(request.getHeader("User-Agent"));
        var record = new LoginRecordEntity();
        record.setUserId(user.getId());
        record.setUsername(user.getUsername());
        record.setNickname(user.getNickname());
        record.setSessionVersion(principal.getSessionVersion());
        record.setSourceIp(ip);
        record.setRegionType(region.type());
        record.setRegion(region.name());
        record.setBrowser(client.browser());
        record.setOs(client.os());
        record.setLoginAt(local(principal.getAuthenticatedAt()));
        record.setLastActivityAt(record.getLoginAt());
        record.setAbsoluteExpiresAt(
                local(principal.getAuthenticatedAt().plus(auth.absoluteTimeout())));
        record.setIdleTimeoutSeconds(Math.toIntExact(auth.idleTimeout().toSeconds()));
        record.setActivityIntervalSeconds(
                Math.toIntExact(properties.activityInterval().toSeconds()));
        mapper.insert(record);
        // Only an independent record ID and timestamp enter the session; never persist its ID.
        request.getSession()
                .setAttribute(
                        SESSION_ATTRIBUTE,
                        new Activity(record.getId(), principal.getAuthenticatedAt()));
    }

    public Activity activity(HttpServletRequest request) {
        var session = request.getSession(false);
        if (session == null) return null;
        Object value = session.getAttribute(SESSION_ATTRIBUTE);
        return value instanceof Activity activity ? activity : null;
    }

    public void observe(HttpServletRequest request) {
        Activity activity = activity(request);
        Instant now = clock.instant();
        if (activity == null
                || now.isBefore(activity.recordedAt().plus(properties.activityInterval()))) return;
        mapper.touch(activity.id(), local(now), local(now.minus(properties.activityInterval())));
        // Shared through Redis Session. SQL also checks the cutoff for concurrent session copies.
        request.getSession(false).setAttribute(SESSION_ATTRIBUTE, new Activity(activity.id(), now));
    }

    public void end(Activity activity, String reason, boolean active) {
        if (activity == null) return;
        Instant now = clock.instant();
        if ("SESSION_INVALIDATED".equals(reason)) {
            LoginRecordEntity row = mapper.selectById(activity.id());
            if (row != null) {
                LoginRecordVo resolved = view(row, null, now);
                if ("EXPIRED".equals(resolved.status())) {
                    mapper.end(
                            activity.id(), resolved.endReason(), local(resolved.endedAt()), false);
                    return;
                }
            }
        }
        mapper.end(activity.id(), reason, local(now), active);
    }

    /** Called inside the account-security mutation's existing transaction. */
    public void endAll(long userId, String reason) {
        mapper.endAll(userId, reason, local(clock.instant()));
    }

    @Transactional(readOnly = true)
    public PageResultVo<LoginRecordVo> mine(PageQueryDto query) {
        UserEntity user = current.requireNormal();
        return page(user.getId(), user, query);
    }

    @Transactional(readOnly = true)
    public PageResultVo<LoginRecordVo> forUser(String id, PageQueryDto query) {
        UserEntity actor = current.requireNormal();
        if (!current.hasModuleAccess(actor.getId(), "user"))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        long userId = rules.id(id);
        UserEntity target = users.getById(userId);
        if (target == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        if (!Objects.equals(actor.getId(), userId)
                && access.isSuperAdmin(userId)
                && !access.isSuperAdmin(actor.getId()))
            throw BusinessException.error(ErrorCode.PROTECTED_ACCOUNT);
        return page(userId, target, query);
    }

    private PageResultVo<LoginRecordVo> page(long userId, UserEntity target, PageQueryDto query) {
        if (query == null) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        var page =
                mapper.selectPage(
                        query.<LoginRecordEntity>toPage(),
                        new LambdaQueryWrapper<LoginRecordEntity>()
                                .eq(LoginRecordEntity::getUserId, userId)
                                .orderByDesc(LoginRecordEntity::getLoginAt)
                                .orderByDesc(LoginRecordEntity::getId));
        Instant now = clock.instant();
        return PageResultVo.from(
                page, page.getRecords().stream().map(row -> view(row, target, now)).toList());
    }

    private LoginRecordVo view(LoginRecordEntity row, UserEntity user, Instant now) {
        String reason = row.getEndReason();
        Instant endedAt = instant(row.getEndedAt());
        Instant last = instant(row.getLastActivityAt());
        Instant absolute = instant(row.getAbsoluteExpiresAt());
        // Persisted activity may lag one interval. The grace prevents falsely expiring an active
        // session.
        Instant idle =
                last.plusSeconds(row.getIdleTimeoutSeconds() + row.getActivityIntervalSeconds());
        if (reason == null) {
            if (!now.isBefore(absolute) && !absolute.isAfter(idle)) {
                reason = "ABSOLUTE_TIMEOUT";
                endedAt = absolute;
            } else if (!now.isBefore(idle)) {
                reason = "IDLE_TIMEOUT";
                endedAt = last.plusSeconds(row.getIdleTimeoutSeconds());
            } else if (!now.isBefore(absolute)) {
                reason = "ABSOLUTE_TIMEOUT";
                endedAt = absolute;
            } else if (user == null) reason = "ACCOUNT_DELETED";
            else if (user.getStatus() == UserStatus.BANNED.getCode()) reason = "ACCOUNT_DISABLED";
            else if (!Objects.equals(user.getSessionVersion(), row.getSessionVersion()))
                reason = "SESSION_INVALIDATED";
            if (reason != null && endedAt == null) endedAt = last;
        }
        String status =
                reason == null ? "ACTIVE" : reason.endsWith("_TIMEOUT") ? "EXPIRED" : "ENDED";
        return new LoginRecordVo(
                row.getId().toString(),
                row.getUserId().toString(),
                row.getUsername(),
                row.getNickname(),
                row.getSourceIp(),
                row.getRegionType(),
                row.getRegion(),
                row.getBrowser(),
                row.getOs(),
                instant(row.getLoginAt()),
                last,
                endedAt,
                reason,
                status,
                Math.max(0, Duration.between(instant(row.getLoginAt()), last).toSeconds()));
    }

    private static LocalDateTime local(Instant value) {
        return LocalDateTime.ofInstant(value, ZONE);
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZONE).toInstant();
    }

    public record Activity(long id, Instant recordedAt) implements Serializable {
        @java.io.Serial private static final long serialVersionUID = 1L;
    }
}
