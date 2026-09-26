package com.streamfusion.platform.auth.guard;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.incrementer.IdentifierGenerator;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.access.service.AccessService;
import com.streamfusion.platform.audit.mapper.AuditReadMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.pojo.vo.AuditReferenceVo;
import com.streamfusion.platform.audit.service.AuditChanges;
import com.streamfusion.platform.audit.service.AuditReferences;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.auth.guard.mapper.IpBlockMapper;
import com.streamfusion.platform.auth.guard.pojo.dto.IpBlockQueryDto;
import com.streamfusion.platform.auth.guard.pojo.entity.IpBlockEntity;
import com.streamfusion.platform.auth.guard.pojo.vo.IpBlockVo;
import com.streamfusion.platform.auth.service.CurrentUserService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.security.ModuleAuthorizationService;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Isolation;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class IpBlockService {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final IpBlockMapper mapper;
    private final IdentifierGenerator ids;
    private final IpGuardProperties properties;
    private final AuditService audit;
    private final AuditReadMapper auditReader;
    private final AuditChanges auditChanges;
    private final AuditReferences references;
    private final Clock clock;
    private final CurrentUserService current;
    private final AccessService access;
    private final AccessMapper accessMapper;
    private final ModuleAuthorizationService authorization;

    public long requireAllowed(String ip) {
        IpBlockEntity row = mapper.find(ip);
        if (row != null && "BLOCKED".equals(row.getStatus()))
            throw BusinessException.error(ErrorCode.IP_BLOCKED);
        return row == null ? 0 : row.getVersion();
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public void block(String ip, long generation, long attempts, AuditContextDto context) {
        IpBlockEntity initial = new IpBlockEntity();
        initial.setId(ids.nextId(initial).longValue());
        initial.setSourceIp(ip);
        initial.setWindowSeconds(Math.toIntExact(properties.failureWindow().toSeconds()));
        initial.setBlockedAt(LocalDateTime.now(clock.withZone(ZONE)));
        mapper.ensure(initial);
        IpBlockEntity row = mapper.lock(ip);
        if (row == null) throw BusinessException.error(ErrorCode.DEPENDENCY_UNAVAILABLE);
        if ("BLOCKED".equals(row.getStatus()) || row.getVersion() != generation) return;
        if (mapper.update(
                        null,
                        new LambdaUpdateWrapper<IpBlockEntity>()
                                .eq(IpBlockEntity::getId, row.getId())
                                .eq(IpBlockEntity::getVersion, generation)
                                .set(IpBlockEntity::getStatus, "BLOCKED")
                                .set(IpBlockEntity::getReasonCode, "LOGIN_FAILURE_THRESHOLD")
                                .set(
                                        IpBlockEntity::getFailedAttempts,
                                        Math.min(attempts, Integer.MAX_VALUE))
                                .set(IpBlockEntity::getWindowSeconds, initial.getWindowSeconds())
                                .set(IpBlockEntity::getBlockedAt, initial.getBlockedAt())
                                .set(IpBlockEntity::getUnblockedAt, null)
                                .set(IpBlockEntity::getUnblockedBy, null)
                                .set(
                                        IpBlockEntity::getVersion,
                                        com.streamfusion.platform.common.validation.VersionCounter
                                                .next(generation)))
                != 1) throw BusinessException.error(ErrorCode.CONFLICT);
        audit.record(
                null,
                "IP_BLOCK",
                row.getId(),
                "IP_BLOCK_CREATE",
                "SUCCESS",
                "LOGIN_FAILURE_THRESHOLD",
                context);
    }

    @Transactional(readOnly = true)
    public PageResultVo<IpBlockVo> page(IpBlockQueryDto query) {
        requireSuper();
        String ip = null;
        if (query.getSourceIp() != null && !query.getSourceIp().isBlank()) {
            try {
                ip = IpAddresses.canonical(query.getSourceIp().strip());
            } catch (IllegalArgumentException ex) {
                throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
            }
        }
        String status = query.getStatus();
        if (status != null && status.isBlank()) status = null;
        if (status != null && !Set.of("BLOCKED", "RELEASED").contains(status))
            throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        var rows =
                mapper.selectPage(
                        query.toPage(),
                        new LambdaQueryWrapper<IpBlockEntity>()
                                .eq(ip != null, IpBlockEntity::getSourceIp, ip)
                                .eq(status != null, IpBlockEntity::getStatus, status)
                                .orderByDesc(IpBlockEntity::getBlockedAt, IpBlockEntity::getId));
        return PageResultVo.from(rows, views(rows.getRecords()));
    }

    @Transactional(isolation = Isolation.READ_COMMITTED)
    public IpBlockVo unblock(String id, String version, AuditContextDto context) {
        long key = DecimalInput.id(id, "id");
        long expected = DecimalInput.version(version, "version");
        if (accessMapper.lockSuperAdminRole() == null)
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        long actor = requireSuper();
        authorization.recheckCurrentRequest(actor);
        IpBlockEntity observed = mapper.selectById(key);
        if (observed == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        IpBlockEntity row = mapper.lock(observed.getSourceIp());
        if (row.getVersion() != expected) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        if (!"BLOCKED".equals(row.getStatus())) throw BusinessException.error(ErrorCode.CONFLICT);
        var now = LocalDateTime.now(clock.withZone(ZONE));
        if (mapper.update(
                        null,
                        new LambdaUpdateWrapper<IpBlockEntity>()
                                .eq(IpBlockEntity::getId, key)
                                .eq(IpBlockEntity::getVersion, expected)
                                .set(IpBlockEntity::getStatus, "RELEASED")
                                .set(IpBlockEntity::getUnblockedAt, now)
                                .set(IpBlockEntity::getUnblockedBy, actor)
                                .set(
                                        IpBlockEntity::getVersion,
                                        com.streamfusion.platform.common.validation.VersionCounter
                                                .next(expected)))
                != 1) throw BusinessException.error(ErrorCode.VERSION_CONFLICT);
        audit.record(actor, "IP_BLOCK", key, "IP_BLOCK_RELEASE", "SUCCESS", null, context);
        return views(List.of(mapper.selectById(key))).getFirst();
    }

    private long requireSuper() {
        long actor = current.requireNormal().getId();
        if (!access.hasModuleAccess(actor, "audit") || !access.isSuperAdmin(actor))
            throw BusinessException.error(ErrorCode.FORBIDDEN);
        return actor;
    }

    private List<IpBlockVo> views(List<IpBlockEntity> rows) {
        Map<Long, Long> releasedBy = new LinkedHashMap<>();
        rows.stream()
                .filter(row -> row.getUnblockedBy() != null)
                .forEach(row -> releasedBy.put(row.getId(), row.getUnblockedBy()));
        Map<Long, AuditReferenceVo> snapshots = new LinkedHashMap<>();
        if (!releasedBy.isEmpty()) {
            for (var release : auditReader.latestIpReleases(List.copyOf(releasedBy.keySet()))) {
                Long expectedActor = releasedBy.get(release.getTargetId());
                // Never attach another actor's or an earlier release's name to the current row.
                if (expectedActor != null && expectedActor.equals(release.getActorId())) {
                    snapshots.put(
                            release.getTargetId(), auditChanges.read(release.getChanges()).actor());
                }
            }
        }
        var currentNames =
                references.current(
                        releasedBy.values().stream()
                                .map(id -> new AuditReferences.Request("USER", id.toString()))
                                .toList());
        return rows.stream()
                .map(
                        row ->
                                view(
                                        row,
                                        references.resolve(
                                                "USER",
                                                row.getUnblockedBy() == null
                                                        ? null
                                                        : row.getUnblockedBy().toString(),
                                                snapshots.get(row.getId()),
                                                currentNames)))
                .toList();
    }

    private static IpBlockVo view(IpBlockEntity row, AuditReferenceVo unblockedByReference) {
        return new IpBlockVo(
                row.getId().toString(),
                row.getSourceIp(),
                row.getStatus(),
                row.getReasonCode(),
                row.getFailedAttempts(),
                row.getWindowSeconds(),
                row.getBlockedAt().atZone(ZONE).toInstant(),
                row.getUnblockedAt() == null ? null : row.getUnblockedAt().atZone(ZONE).toInstant(),
                row.getUnblockedBy() == null ? null : row.getUnblockedBy().toString(),
                unblockedByReference,
                row.getVersion().toString());
    }
}
