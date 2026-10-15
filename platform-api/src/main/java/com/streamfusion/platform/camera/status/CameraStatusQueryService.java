package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.vo.CameraAddressVo;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.security.MessageDigest;
import java.time.Clock;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class CameraStatusQueryService {
    private static final int MAX_PAGE_PROFILES = 3200;
    private static final ZoneId ZONE = ZoneId.of("Asia/Shanghai");
    private static final String PROBE = "RTSP_DESCRIBE";
    private final CameraStatusCheckMapper checks;
    private final CameraStatusSnapshotMapper snapshots;
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraAccessService cameras;
    private final CameraCryptoService crypto;
    private final CameraRtspTargetService targets;
    private final Clock clock;

    @Transactional(readOnly = true)
    public CameraStatusSnapshotVo status(String cameraId) {
        long id = DecimalInput.id(cameraId, "cameraId");
        var actor = cameras.readActor();
        cameras.requireVisible(id, actor);
        var camera = channels.selectById(id);
        if (camera == null) throw error(ErrorCode.NOT_FOUND);
        var selected =
                profiles.selectList(
                        new LambdaQueryWrapper<CameraProfileEntity>()
                                .eq(CameraProfileEntity::getChannelId, id)
                                .orderByAsc(CameraProfileEntity::getId));
        var facts = new HashMap<Long, CameraStatusSnapshotEntity>();
        for (var fact :
                snapshots.selectList(
                        new LambdaQueryWrapper<CameraStatusSnapshotEntity>()
                                .eq(CameraStatusSnapshotEntity::getCameraId, id)))
            facts.put(fact.getProfileId(), fact);
        var latest = new HashMap<Long, CameraStatusCheckEntity>();
        for (var check : checks.latest(id)) latest.put(check.getProfileId(), check);
        var result = new ArrayList<CameraStatusSnapshotVo.Profile>();
        Instant now = nowInstant();
        for (var profile : selected) {
            var fact = facts.get(profile.getId());
            CameraRtspTargetService.Inspection inspection;
            try {
                inspection = targets.inspect(camera, profile, crypto.activeKeyId(), () -> {});
            } catch (BusinessException ex) {
                inspection =
                        new CameraRtspTargetService.Inspection(
                                null, null, "CONFIGURATION_UNAVAILABLE");
            }
            boolean configurationCurrent = false;
            if (fact != null) {
                var original =
                        targets.inspect(camera, profile, fact.getConnectionKeyId(), () -> {});
                configurationCurrent =
                        original.connectionDigest() != null
                                && MessageDigest.isEqual(
                                        fact.getConnectionDigest(), original.connectionDigest());
            }
            String freshness =
                    fact == null
                            ? "NEVER"
                            : configurationCurrent && now.isBefore(instant(fact.getValidUntil()))
                                    ? "CURRENT"
                                    : "STALE";
            result.add(
                    new CameraStatusSnapshotVo.Profile(
                            profile.getId().toString(),
                            "CONTROL",
                            PROBE,
                            fact == null ? null : CameraStatusObservationVo.from(fact),
                            freshness,
                            configurationCurrent,
                            latest.containsKey(profile.getId())
                                    ? CameraStatusCheckVo.from(latest.get(profile.getId()))
                                    : null,
                            inspection.blockingReasonCode() == null,
                            inspection.blockingReasonCode(),
                            configurationCurrent ? CameraStatusTimelineVo.from(fact) : null));
        }
        return new CameraStatusSnapshotVo(Long.toString(id), now, result);
    }

    @Transactional(readOnly = true)
    public CameraStatusCheckVo check(String cameraId, String checkId) {
        long id = DecimalInput.id(cameraId, "cameraId");
        cameras.requireVisible(id, cameras.readActor());
        var check = checks.selectById(DecimalInput.id(checkId, "checkId"));
        if (check == null || check.getCameraId() != id) throw error(ErrorCode.NOT_FOUND);
        return CameraStatusCheckVo.from(check);
    }

    @Transactional(readOnly = true)
    public CameraStatusPageVo page(CameraStatusPageQuery query) {
        if (query == null) throw error(ErrorCode.VALIDATION_ERROR);
        var actor = cameras.readActor();
        String name = query.getName() == null ? null : query.getName().strip();
        if (name != null) {
            if (name.codePointCount(0, name.length()) > 128
                    || name.codePoints().anyMatch(Character::isISOControl))
                throw error(ErrorCode.VALIDATION_ERROR);
            name =
                    name.isEmpty()
                            ? null
                            : name.replace("!", "!!").replace("%", "!%").replace("_", "!_");
        }
        String observation = query.getObservation();
        if (observation != null
                && !Set.of("NEVER", "ACCESSIBLE", "FAILED", "UNSUPPORTED").contains(observation))
            throw error(ErrorCode.VALIDATION_ERROR);
        var page =
                channels.pageStatus(query.toPage(), name, observation, cameras.visibility(actor));
        var rows = page.getRecords();
        var profilesByCamera = profileStatuses(rows, false);
        var items = new ArrayList<CameraStatusPageVo.Item>();
        for (var row : rows)
            items.add(
                    new CameraStatusPageVo.Item(
                            row.getId().toString(),
                            row.getDeviceId() == null ? "c" + row.getId() : "d" + row.getDeviceId(),
                            row.getName(),
                            row.getDeviceName(),
                            CameraAddressVo.of(
                                    row.getConnectionCategory(),
                                    row.getAddressHost(),
                                    row.getAddressPort()),
                            row.getLifecycle(),
                            row.getDefaultPreviewProfileId() == null
                                    ? null
                                    : row.getDefaultPreviewProfileId().toString(),
                            profilesByCamera.getOrDefault(row.getId(), List.of())));
        return new CameraStatusPageVo(
                items,
                page.getTotal(),
                query.getPage(),
                query.getSize(),
                nowInstant(),
                summary(items));
    }

    private static CameraStatusPageVo.Summary summary(List<CameraStatusPageVo.Item> items) {
        int accessible = 0, failed = 0, checking = 0, stale = 0, unknown = 0, disabled = 0;
        for (var item : items) {
            if (!"ENABLED".equals(item.lifecycle())) {
                disabled++;
                continue;
            }
            var profile = representativeProfile(item);
            if (profile == null) unknown++;
            else if (profile.latestCheck() != null
                    && Set.of("QUEUED", "RUNNING").contains(profile.latestCheck().executionState()))
                checking++;
            else if ("STALE".equals(profile.freshness())
                    || (profile.observation() != null && !profile.configurationCurrent())) stale++;
            else if ("CURRENT".equals(profile.freshness()) && profile.observation() != null) {
                if ("ACCESSIBLE".equals(profile.observation().value())) accessible++;
                else if ("FAILED".equals(profile.observation().value())) failed++;
                else unknown++;
            } else unknown++;
        }
        return new CameraStatusPageVo.Summary(
                items.size(), accessible, failed, checking, stale, unknown, disabled);
    }

    private static CameraStatusPageVo.Profile representativeProfile(CameraStatusPageVo.Item item) {
        var enabled = item.profiles().stream().filter(CameraStatusPageVo.Profile::enabled).toList();
        return enabled.stream()
                .filter(
                        profile ->
                                Objects.equals(
                                        profile.streamProfileId(), item.defaultPreviewProfileId()))
                .findFirst()
                .orElseGet(
                        () ->
                                enabled.stream()
                                        .filter(profile -> "MAIN".equals(profile.streamKind()))
                                        .findFirst()
                                        .orElse(enabled.isEmpty() ? null : enabled.getFirst()));
    }

    public Map<String, CameraDeviceStatusVo> deviceSummaries(
            List<com.streamfusion.platform.camera.mapper.CameraDeviceGroupRow> groups,
            CameraAccessService.Visibility visibility) {
        if (groups.isEmpty()) return Map.of();
        var rows =
                channels.statusDeviceRepresentatives(
                        groups.stream().map(row -> row.getGroupKey()).toList(), visibility, 3201);
        if (rows.size() > 3200) throw error(ErrorCode.CONFLICT);
        var facts = profileStatuses(rows, true);
        var grouped = new HashMap<String, CameraStatusPageVo.Profile>();
        for (var row : rows) {
            if (!"ENABLED".equals(row.getLifecycle())) continue;
            String key = row.getDeviceId() == null ? "c" + row.getId() : "d" + row.getDeviceId();
            var candidates = facts.getOrDefault(row.getId(), List.of());
            var profile = candidates.isEmpty() ? null : candidates.getFirst();
            grouped.put(key, profile);
        }
        var result = new HashMap<String, CameraDeviceStatusVo>();
        for (var group : groups) {
            int reachable = 0, failed = 0, unknown = 0;
            boolean stale = false, checking = false;
            Instant checked = null, until = null;
            var profile = grouped.get(group.getGroupKey());
            if (profile == null) {
                unknown = grouped.containsKey(group.getGroupKey()) ? 1 : 0;
            } else {
                checking =
                        profile.latestCheck() != null
                                && Set.of("QUEUED", "RUNNING")
                                        .contains(profile.latestCheck().executionState());
                if (profile.observation() != null) {
                    checked = profile.observation().observedAt();
                }
                if (!"CURRENT".equals(profile.freshness()) || !profile.configurationCurrent()) {
                    stale = "STALE".equals(profile.freshness());
                    unknown = 1;
                } else {
                    until = profile.observation().validUntil();
                    if ("ACCESSIBLE".equals(profile.observation().value())) reachable = 1;
                    else if ("FAILED".equals(profile.observation().value())) failed = 1;
                    else unknown = 1;
                }
            }
            String state =
                    checking
                            ? "CHECKING"
                            : failed > 0
                                    ? "FAILED"
                                    : reachable > 0 ? "ACCESSIBLE" : stale ? "STALE" : "NEVER";
            result.put(
                    group.getGroupKey(),
                    new CameraDeviceStatusVo(state, checked, until, reachable, failed, unknown));
        }
        return result;
    }

    private Map<Long, List<CameraStatusPageVo.Profile>> profileStatuses(
            List<? extends CameraChannelEntity> rows, boolean representatives) {
        var cameraIds = rows.stream().map(CameraChannelEntity::getId).toList();
        var profileRows =
                cameraIds.isEmpty()
                        ? List.<CameraProfileEntity>of()
                        : representatives
                                ? profiles.statusRepresentatives(cameraIds)
                                : profiles.statusProfiles(cameraIds, MAX_PAGE_PROFILES + 1);
        if (profileRows.size() > MAX_PAGE_PROFILES) throw error(ErrorCode.CONFLICT);
        var profileCounts = new HashMap<Long, Integer>();
        for (var profile : profileRows)
            profileCounts.merge(profile.getChannelId(), 1, Integer::sum);
        if (profileCounts.values().stream().anyMatch(count -> count > 1000))
            throw error(ErrorCode.CONFLICT);
        var profileIds = profileRows.stream().map(CameraProfileEntity::getId).toList();
        var facts =
                profileIds.isEmpty()
                        ? List.<CameraStatusSnapshotEntity>of()
                        : snapshots.selectBatchIds(profileIds);
        var latestRows =
                profileIds.isEmpty()
                        ? List.<CameraStatusCheckEntity>of()
                        : checks.latestForProfiles(profileIds);
        var factByProfile = new HashMap<Long, CameraStatusSnapshotEntity>();
        for (var fact : facts) factByProfile.put(fact.getProfileId(), fact);
        var latestByProfile = new HashMap<Long, CameraStatusCheckEntity>();
        for (var latestCheck : latestRows)
            latestByProfile.put(latestCheck.getProfileId(), latestCheck);
        var observedKeys = new HashMap<Long, String>();
        for (var fact : facts) observedKeys.put(fact.getProfileId(), fact.getConnectionKeyId());
        String activeKey = null;
        try {
            activeKey = crypto.activeKeyId();
        } catch (BusinessException ex) {
            /* Existing facts still use their own retained key below. */
        }
        var inspections = targets.inspectBatch(rows, profileRows, activeKey, observedKeys);
        var profilesByCamera = new HashMap<Long, List<CameraStatusPageVo.Profile>>();
        Instant serverNow = nowInstant();
        for (var profile : profileRows) {
            var fact = factByProfile.get(profile.getId());
            var currentInspection = inspections.current().get(profile.getId());
            var observedInspection = inspections.observed().get(profile.getId());
            boolean current =
                    fact != null
                            && observedInspection != null
                            && observedInspection.connectionDigest() != null
                            && MessageDigest.isEqual(
                                    fact.getConnectionDigest(),
                                    observedInspection.connectionDigest());
            String freshness =
                    fact == null
                            ? "NEVER"
                            : current && serverNow.isBefore(instant(fact.getValidUntil()))
                                    ? "CURRENT"
                                    : "STALE";
            var item =
                    new CameraStatusPageVo.Profile(
                            profile.getId().toString(),
                            profile.getLabel(),
                            profile.getUsageHint(),
                            Boolean.TRUE.equals(profile.getEnabled()),
                            "CONTROL",
                            PROBE,
                            fact == null ? null : CameraStatusObservationVo.from(fact),
                            freshness,
                            current,
                            latestByProfile.containsKey(profile.getId())
                                    ? CameraStatusCheckVo.from(latestByProfile.get(profile.getId()))
                                    : null,
                            currentInspection != null
                                    && currentInspection.blockingReasonCode() == null,
                            currentInspection == null
                                    ? "CONFIGURATION_UNAVAILABLE"
                                    : currentInspection.blockingReasonCode(),
                            current ? CameraStatusTimelineVo.from(fact) : null);
            profilesByCamera
                    .computeIfAbsent(profile.getChannelId(), ignored -> new ArrayList<>())
                    .add(item);
        }
        return profilesByCamera;
    }

    private Instant nowInstant() {
        return clock.instant().truncatedTo(ChronoUnit.MILLIS);
    }

    private LocalDateTime now() {
        return LocalDateTime.ofInstant(nowInstant(), ZONE);
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(ZONE).toInstant();
    }

    private static BusinessException error(ErrorCode code) {
        return BusinessException.error(code);
    }
}
