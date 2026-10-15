package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.baomidou.mybatisplus.core.conditions.update.LambdaUpdateWrapper;
import com.baomidou.mybatisplus.core.incrementer.DefaultIdentifierGenerator;
import com.streamfusion.platform.access.mapper.AccessMapper;
import com.streamfusion.platform.audit.pojo.dto.AuditContextDto;
import com.streamfusion.platform.audit.service.AuditService;
import com.streamfusion.platform.camera.access.adapter.CameraAdapterException;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRequestKey;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Duration;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.temporal.ChronoUnit;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.TransactionTemplate;

/** Fixed targets and finite slots. Dispatch is small and never replays missed slots. */
@Service
@RequiredArgsConstructor
public class CameraStatusPlanService {
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraStatusPlanMapper plans;
    private final CameraStatusPlanTargetMapper targets;
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraAccessService access;
    private final AccessMapper coordination;
    private final CameraCryptoService crypto;
    private final CameraRtspTargetService rtsp;
    private final CameraStatusService status;
    private final CameraStatusProperties properties;
    private final PlatformTransactionManager manager;
    private final Clock clock;
    private final AuditService audit;

    @Transactional
    public CameraStatusPlanVo create(CameraStatusPlanCommand command, AuditContextDto context) {
        if (command == null
                || command.groupKeys() == null
                || command.groupKeys().isEmpty()
                || command.groupKeys().size() > 2000
                || command.intervalMinutes() == null
                || command.intervalMinutes() < 5
                || command.intervalMinutes() > 1440
                || command.durationHours() == null
                || command.durationHours() < 1
                || command.durationHours() > 168
                || command.durationHours() * 60 < command.intervalMinutes())
            throw error(ErrorCode.VALIDATION_ERROR);
        if (command.groupKeys().stream().anyMatch(Objects::isNull))
            throw error(ErrorCode.VALIDATION_ERROR);
        var keys = command.groupKeys().stream().distinct().sorted().toList();
        for (String key : keys) {
            if (key == null || !key.matches("[dc][1-9][0-9]{0,18}"))
                throw error(ErrorCode.VALIDATION_ERROR);
            DecimalInput.id(key.substring(1), "groupKey");
        }
        var requested = CameraRequestKey.timestamp(command.clientRequestId());
        var actor = access.lockActor();
        var input =
                Map.of(
                        "keys",
                        keys,
                        "interval",
                        command.intervalMinutes(),
                        "hours",
                        command.durationHours());
        var prior = plans.byRequest(actor.userId(), command.clientRequestId());
        if (prior != null) {
            if (!MessageDigest.isEqual(
                    prior.getRequestFingerprint(),
                    crypto.digest(prior.getRequestKeyId(), "camera-status-plan-request", input)))
                throw error(ErrorCode.CONFLICT).diagnosed("PLAN_REQUEST_REUSED");
            return view(prior);
        }
        CameraRequestKey.requireFresh(requested, clock.instant());
        if (plans.selectCount(
                        new LambdaQueryWrapper<CameraStatusPlanEntity>()
                                .eq(CameraStatusPlanEntity::getActorUserId, actor.userId())
                                .eq(CameraStatusPlanEntity::getState, "ACTIVE")
                                .gt(CameraStatusPlanEntity::getEndAt, now()))
                >= 10) throw error(ErrorCode.RATE_LIMITED).diagnosed("PLAN_ACTIVE_LIMIT");
        var rows = channels.statusDeviceRepresentatives(keys, access.visibility(actor), 2001);
        if (rows.isEmpty() || rows.size() > 2000)
            throw error(ErrorCode.CONFLICT).diagnosed("PLAN_TARGET_RANGE_INVALID");
        var present =
                rows.stream()
                        .map(
                                row ->
                                        row.getDeviceId() == null
                                                ? "c" + row.getId()
                                                : "d" + row.getDeviceId())
                        .distinct()
                        .sorted()
                        .toList();
        if (!present.equals(keys))
            throw error(ErrorCode.NOT_FOUND).diagnosed("PLAN_TARGET_UNAVAILABLE");
        var chosen = profiles.statusRepresentatives(rows.stream().map(row -> row.getId()).toList());
        if (chosen.size() != rows.size())
            throw error(ErrorCode.CONFLICT).diagnosed("PLAN_PROFILE_MISSING");
        String keyId = crypto.activeKeyId();
        var inspections = rtsp.inspectBatch(rows, chosen, keyId, Map.of()).current();
        if (chosen.stream()
                .anyMatch(
                        p ->
                                inspections.get(p.getId()) == null
                                        || inspections.get(p.getId()).blockingReasonCode() != null))
            throw error(ErrorCode.CONFLICT).diagnosed("PLAN_TARGET_NOT_CHECKABLE");
        double perMinute =
                Math.min(
                        60.0,
                        Math.min(
                                60_000.0 / properties.getStartIntervalMs(),
                                properties.getWorkerThreads()
                                        * 60_000.0
                                        / properties.getProbeTimeoutMs()));
        if (plans.targetsPerMinute(now()) + rows.size() * 1.0 / command.intervalMinutes()
                > perMinute)
            throw error(ErrorCode.RATE_LIMITED).diagnosed("PLAN_CAPACITY_EXCEEDED");
        var plan = new CameraStatusPlanEntity();
        plan.setId(DefaultIdentifierGenerator.getInstance().nextId(plan).longValue());
        plan.setActorUserId(actor.userId());
        plan.setClientRequestId(command.clientRequestId());
        plan.setRequestKeyId(keyId);
        plan.setRequestFingerprint(crypto.digest(keyId, "camera-status-plan-request", input));
        plan.setState("ACTIVE");
        plan.setIntervalMinutes(command.intervalMinutes());
        plan.setTargetCount(chosen.size());
        plan.setStartAt(now().plusSeconds(1));
        plan.setEndAt(plan.getStartAt().plusHours(command.durationHours()));
        plan.setCreatedAt(now());
        plan.setUpdatedAt(now());
        plan.setVersion(0L);
        plans.insert(plan);
        long intervalMs = command.intervalMinutes() * 60_000L;
        for (int index = 0; index < chosen.size(); index++) {
            var profile = chosen.get(index);
            var target = new CameraStatusPlanTargetEntity();
            target.setId(DefaultIdentifierGenerator.getInstance().nextId(target).longValue());
            target.setPlanId(plan.getId());
            target.setCameraId(profile.getChannelId());
            target.setProfileId(profile.getId());
            target.setConnectionKeyId(keyId);
            target.setConnectionDigest(inspections.get(profile.getId()).connectionDigest());
            target.setNextAt(
                    plan.getStartAt().plusNanos(index * intervalMs / chosen.size() * 1_000_000));
            target.setBlocked(false);
            targets.insert(target);
        }
        audit.record(
                actor.userId(),
                "CAMERA_STATUS_PLAN",
                plan.getId(),
                "CAMERA_STATUS_PLAN_CREATE",
                "SUCCESS",
                null,
                context,
                Map.of(
                        "name",
                        "巡检任务 " + plan.getId(),
                        "targetCount",
                        Integer.toString(plan.getTargetCount()),
                        "intervalMinutes",
                        Integer.toString(command.intervalMinutes()),
                        "durationHours",
                        Integer.toString(command.durationHours()),
                        "afterState",
                        "ACTIVE"));
        return view(plan);
    }

    @Transactional(readOnly = true)
    public List<CameraStatusPlanVo> list() {
        var actor = access.readActor();
        return plans.forActor(actor.userId()).stream().map(this::view).toList();
    }

    @Transactional
    public CameraStatusPlanVo stop(String id, String version, AuditContextDto context) {
        var actor = access.lockActor();
        var plan = plans.lock(DecimalInput.id(id, "planId"));
        if (plan == null || plan.getActorUserId() != actor.userId())
            throw error(ErrorCode.NOT_FOUND);
        if (plan.getVersion() != DecimalInput.version(version, "version"))
            throw error(ErrorCode.CONFLICT).diagnosed("PLAN_VERSION_CHANGED");
        if ("ACTIVE".equals(plan.getState())) {
            plan.setState("STOPPED");
            plan.setVersion(plan.getVersion() + 1);
            plan.setUpdatedAt(now());
            plans.updateById(plan);
            audit.record(
                    actor.userId(),
                    "CAMERA_STATUS_PLAN",
                    plan.getId(),
                    "CAMERA_STATUS_PLAN_STOP",
                    "SUCCESS",
                    null,
                    context,
                    Map.of(
                            "name",
                            "巡检任务 " + plan.getId(),
                            "beforeState",
                            "ACTIVE",
                            "afterState",
                            "STOPPED"));
        }
        return view(plan);
    }

    public void tick() {
        plans.update(
                null,
                new LambdaUpdateWrapper<CameraStatusPlanEntity>()
                        .eq(CameraStatusPlanEntity::getState, "ACTIVE")
                        .le(CameraStatusPlanEntity::getEndAt, now())
                        .set(CameraStatusPlanEntity::getState, "EXPIRED")
                        .set(CameraStatusPlanEntity::getUpdatedAt, now())
                        .setIncrBy(CameraStatusPlanEntity::getVersion, 1));
        for (var due : targets.due(now())) {
            Long checkId =
                    new TransactionTemplate(manager)
                            .execute(
                                    tx -> {
                                        coordination.lockSuperAdminRole();
                                        var plan = plans.lock(due.getPlanId());
                                        var target = targets.lock(due.getId());
                                        if (plan == null
                                                || target == null
                                                || !"ACTIVE".equals(plan.getState())
                                                || target.getBlocked()
                                                || !now().isBefore(plan.getEndAt())
                                                || now().isBefore(target.getNextAt())) return null;
                                        var slot = target.getNextAt();
                                        long interval = plan.getIntervalMinutes() * 60_000L;
                                        long slots =
                                                Math.max(
                                                        1,
                                                        Duration.between(slot, now()).toMillis()
                                                                        / interval
                                                                + 1);
                                        target.setNextAt(
                                                slot.plusNanos(slots * interval * 1_000_000));
                                        Long created = null;
                                        if (!status.planActorAllowed(
                                                plan.getActorUserId(), target.getCameraId())) {
                                            target.setBlocked(true);
                                            target.setReasonCode("ACTOR_REVOKED");
                                        } else if (slot.isBefore(now().minusSeconds(30))) {
                                            target.setReasonCode("MISSED_SLOT");
                                        } else {
                                            try {
                                                var check =
                                                        status.createScheduled(plan, target, slot);
                                                created = Long.valueOf(check.checkId());
                                                target.setLastCheckId(created);
                                                target.setReasonCode(null);
                                            } catch (BusinessException ex) {
                                                target.setReasonCode("CONFIGURATION_UNAVAILABLE");
                                            } catch (CameraAdapterException ex) {
                                                target.setReasonCode(ex.reasonCode());
                                                if ("CONFIGURATION_CHANGED".equals(ex.reasonCode()))
                                                    target.setBlocked(true);
                                            }
                                        }
                                        targets.updateById(target);
                                        return created;
                                    });
            if (checkId != null) status.dispatch(checkId, null);
        }
        var old =
                plans.selectList(
                        new LambdaQueryWrapper<CameraStatusPlanEntity>()
                                .ne(CameraStatusPlanEntity::getState, "ACTIVE")
                                .le(
                                        CameraStatusPlanEntity::getEndAt,
                                        now().minusDays(properties.getRetentionDays()))
                                .orderByAsc(CameraStatusPlanEntity::getEndAt)
                                .last("LIMIT 20"));
        for (var plan : old)
            new TransactionTemplate(manager)
                    .execute(
                            tx -> {
                                targets.delete(
                                        new LambdaQueryWrapper<CameraStatusPlanTargetEntity>()
                                                .eq(
                                                        CameraStatusPlanTargetEntity::getPlanId,
                                                        plan.getId()));
                                plans.deleteById(plan.getId());
                                return null;
                            });
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(clock.instant(), ZONE).truncatedTo(ChronoUnit.MILLIS);
    }

    private CameraStatusPlanVo view(CameraStatusPlanEntity plan) {
        return new CameraStatusPlanVo(
                plan.getId().toString(),
                plan.getState(),
                plan.getIntervalMinutes(),
                plan.getTargetCount(),
                plan.getStartAt().atZone(ZONE).toInstant(),
                plan.getEndAt().atZone(ZONE).toInstant(),
                plan.getVersion().toString(),
                plan.getBlockedCount() == null ? 0 : plan.getBlockedCount(),
                "ACTIVE".equals(plan.getState()) && plan.getNextAt() != null
                        ? plan.getNextAt().atZone(ZONE).toInstant()
                        : null,
                plan.getLastReasonCode(),
                plan.getSuccessCount() == null
                        ? null
                        : new CameraStatusPlanVo.Progress(
                                plan.getSuccessCount(),
                                plan.getFailedCount(),
                                plan.getPendingCount(),
                                plan.getSkippedCount(),
                                plan.getUnknownCount()),
                clock.instant());
    }

    private static BusinessException error(ErrorCode code) {
        return BusinessException.error(code);
    }
}
