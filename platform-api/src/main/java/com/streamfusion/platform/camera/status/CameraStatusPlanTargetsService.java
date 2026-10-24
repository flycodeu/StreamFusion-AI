package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.mapper.CameraScopeMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.function.Function;
import java.util.stream.Collectors;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

/** Configuration preview only. Creation and execution still recheck authorization and targets. */
@Service
@RequiredArgsConstructor
public class CameraStatusPlanTargetsService {
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraScopeMapper scopes;
    private final CameraStatusPlanMapper plans;
    private final CameraStatusPlanTargetMapper targets;
    private final CameraAccessService access;
    private final CameraCryptoService crypto;
    private final CameraRtspTargetService rtsp;
    private final com.streamfusion.platform.camera.service.CameraGroupService groups;

    public record Query(
            @Schema(description = "核对范围：ALL为当前授权全量，GROUPS为视频分组，PAGE为指定设备，PLAN为已有任务") String scope,
            @Schema(description = "指定设备的分组键，仅PAGE范围填写") List<String> groupKeys,
            @Schema(description = "已有任务编号，仅PLAN范围填写") String planId,
            @Schema(description = "视频分组ID，仅GROUPS范围填写，包含子分组") List<String> groupIds,
            @Schema(description = "按设备覆盖默认检测通道和码流")
                    List<CameraStatusPlanCommand.Selection> targetSelections) {
        public Query(String scope, List<String> groupKeys, String planId, List<String> groupIds) {
            this(scope, groupKeys, planId, groupIds, null);
        }
    }

    public record Target(
            @Schema(description = "设备或独立通道的分组键") String groupKey,
            @Schema(description = "目标名称") String name,
            @Schema(description = "不可检测的原因码，可检测时为空") String reasonCode,
            @Schema(description = "设备所有已归档成员的分组ID，不依赖树展开") List<String> groupIds,
            @Schema(description = "实际选中的检测通道ID") String cameraId,
            @Schema(description = "检测通道名称") String channelName,
            @Schema(description = "实际选中的码流ID") String streamProfileId,
            @Schema(description = "检测码流名称") String profileName,
            @Schema(description = "是否支持直连视频检测；平台目标为false") boolean videoSupported) {}

    public record Preview(
            @Schema(description = "范围内各目标的配置核对结果") List<Target> targets,
            @Schema(description = "范围是否超过单个任务2000个目标的上限") boolean limitExceeded,
            @Schema(description = "当前授权范围全部启用设备数，用于树根节点全选状态") long totalCount) {}

    @Transactional(readOnly = true)
    public Preview preview(Query query) {
        if (query == null || query.scope() == null) throw invalid();
        var overrides = selections(query.targetSelections());
        var actor = access.readActor();
        long totalCount = scopes.visibleDeviceCount(access.visibility(actor), true);
        List<String> keys;
        List<Long> selectedGroups = null;
        List<Long> fixedChannels = null;
        List<CameraStatusPlanTargetEntity> fixedTargets = List.of();
        if (!"GROUPS".equals(query.scope()) && query.groupIds() != null) throw invalid();
        switch (query.scope()) {
            case "GROUPS" -> {
                if (query.groupKeys() != null
                        || query.planId() != null
                        || query.groupIds() == null
                        || query.groupIds().isEmpty()
                        || query.groupIds().size() > 100) throw invalid();
                selectedGroups =
                        groups.queryGroupIds(
                                query.groupIds().stream()
                                        .map(id -> DecimalInput.id(id, "groupId"))
                                        .toList(),
                                true);
                if (selectedGroups.isEmpty()) return new Preview(List.of(), false, totalCount);
                keys = null;
            }
            case "ALL" -> {
                if (query.groupKeys() != null || query.planId() != null) throw invalid();
                keys = null;
            }
            case "PAGE" -> {
                if (query.planId() != null
                        || query.groupKeys() == null
                        || query.groupKeys().size() > 2000) throw invalid();
                keys = query.groupKeys().stream().distinct().toList();
                for (String key : keys) {
                    if (key == null || !key.matches("[dc][1-9][0-9]{0,18}")) throw invalid();
                    DecimalInput.id(key.substring(1), "groupKey");
                }
            }
            case "PLAN" -> {
                if (query.groupKeys() != null) throw invalid();
                var plan = plans.selectById(DecimalInput.id(query.planId(), "planId"));
                if (plan == null || plan.getActorUserId() != actor.userId())
                    throw BusinessException.error(ErrorCode.NOT_FOUND);
                keys = targets.groupKeys(plan.getId());
                fixedTargets = targets.forPlan(plan.getId());
                fixedChannels =
                        fixedTargets.stream()
                                .map(CameraStatusPlanTargetEntity::getCameraId)
                                .toList();
                if (keys.size() != plan.getTargetCount())
                    throw BusinessException.error(ErrorCode.CONFLICT);
            }
            default -> throw invalid();
        }
        if (keys != null && keys.isEmpty()) {
            if (!overrides.isEmpty()) throw invalid();
            return new Preview(List.of(), false, totalCount);
        }
        var rows =
                fixedChannels != null
                        ? channels.statusSelectedChannels(fixedChannels, access.visibility(actor))
                        : selectedGroups == null
                                ? channels.statusDeviceRepresentatives(
                                        keys, access.visibility(actor), 2001)
                                : channels.statusGroupRepresentatives(
                                        selectedGroups, access.visibility(actor), 2001);
        // Never silently present a truncated all-device scope as a complete patrol.
        if (rows.size() > 2000) return new Preview(List.of(), true, totalCount);
        var resolved = resolve(rows, fixedTargets, overrides, access.visibility(actor));
        rows = resolved.rows();
        var chosen = resolved.profiles();
        var byChannel =
                chosen.stream()
                        .collect(
                                Collectors.toMap(
                                        profile -> profile.getChannelId(), Function.identity()));
        var inspections = rtsp.inspectBatch(rows, chosen, crypto.activeKeyId(), Map.of()).current();
        var result = new ArrayList<Target>();
        var placements =
                scopes.deviceGroups(
                        rows.stream()
                                .map(CameraChannelEntity::getDeviceId)
                                .filter(java.util.Objects::nonNull)
                                .distinct()
                                .toList());
        if (placements.size() > 10000) throw BusinessException.error(ErrorCode.CONFLICT);
        Map<Long, List<String>> groupIds = new HashMap<>();
        for (var placement : placements)
            groupIds.computeIfAbsent(placement.getDeviceId(), id -> new ArrayList<>())
                    .add(placement.getGroupId().toString());
        var platformProfiles =
                chosen.isEmpty()
                        ? java.util.Set.<Long>of()
                        : rtsp.platformProfiles(
                                chosen.stream().map(CameraProfileEntity::getId).toList());
        for (var row : rows) {
            String key = row.getDeviceId() == null ? "c" + row.getId() : "d" + row.getDeviceId();
            var profile = byChannel.get(row.getId());
            var inspection = profile == null ? null : inspections.get(profile.getId());
            String reason =
                    profile == null
                            ? "PROFILE_NOT_CONFIGURED"
                            : inspection == null
                                    ? "CONFIGURATION_UNAVAILABLE"
                                    : inspection.blockingReasonCode();
            result.add(
                    new Target(
                            key,
                            row.getDeviceName() == null ? row.getName() : row.getDeviceName(),
                            reason,
                            row.getDeviceId() == null
                                    ? row.getGroupId() == null
                                            ? List.of()
                                            : List.of(row.getGroupId().toString())
                                    : groupIds.getOrDefault(row.getDeviceId(), List.of()),
                            row.getId().toString(),
                            row.getName(),
                            profile == null ? null : profile.getId().toString(),
                            profile == null ? null : profile.getLabel(),
                            profile == null || !platformProfiles.contains(profile.getId())));
        }
        if (keys != null) {
            var present = result.stream().map(Target::groupKey).collect(Collectors.toSet());
            for (String key : keys)
                if (!present.contains(key))
                    result.add(
                            new Target(
                                    key,
                                    key,
                                    "TARGET_UNAVAILABLE",
                                    List.of(),
                                    null,
                                    null,
                                    null,
                                    null,
                                    false));
        }
        return new Preview(List.copyOf(result), false, totalCount);
    }

    private static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }

    static List<CameraStatusPlanCommand.Selection> selections(
            List<CameraStatusPlanCommand.Selection> input) {
        if (input == null) return List.of();
        if (input.size() > 2000) throw invalid();
        var keys = new HashSet<String>();
        for (var item : input) {
            if (item == null
                    || item.groupKey() == null
                    || !item.groupKey().matches("[dc][1-9][0-9]{0,18}")
                    || !keys.add(item.groupKey())) throw invalid();
            DecimalInput.id(item.groupKey().substring(1), "groupKey");
            DecimalInput.id(item.cameraId(), "cameraId");
            DecimalInput.id(item.streamProfileId(), "streamProfileId");
        }
        return input.stream()
                .sorted(Comparator.comparing(CameraStatusPlanCommand.Selection::groupKey))
                .toList();
    }

    record Resolved(List<CameraStatusPageRow> rows, List<CameraProfileEntity> profiles) {}

    /**
     * Shared by preview and creation. All replacements must stay in the authorized device range.
     */
    Resolved resolve(
            List<CameraStatusPageRow> defaults,
            List<CameraStatusPlanTargetEntity> fixed,
            List<CameraStatusPlanCommand.Selection> selections,
            CameraAccessService.Visibility visibility) {
        var rows = new java.util.LinkedHashMap<String, CameraStatusPageRow>();
        for (var row : defaults) rows.put(deviceKey(row), row);
        var explicitProfiles = new HashMap<Long, Long>();
        for (var target : fixed) explicitProfiles.put(target.getCameraId(), target.getProfileId());
        if (!selections.isEmpty()) {
            var requested =
                    channels
                            .statusSelectedChannels(
                                    selections.stream()
                                            .map(s -> DecimalInput.id(s.cameraId(), "cameraId"))
                                            .distinct()
                                            .toList(),
                                    visibility)
                            .stream()
                            .collect(
                                    Collectors.toMap(
                                            CameraChannelEntity::getId, Function.identity()));
            for (var selection : selections) {
                var row = requested.get(DecimalInput.id(selection.cameraId(), "cameraId"));
                if (!rows.containsKey(selection.groupKey())
                        || row == null
                        || !deviceKey(row).equals(selection.groupKey()))
                    throw BusinessException.error(ErrorCode.NOT_FOUND)
                            .diagnosed("PLAN_TARGET_UNAVAILABLE");
                var original = rows.get(selection.groupKey());
                row.setDeviceName(original.getDeviceName());
                rows.put(selection.groupKey(), row);
                explicitProfiles.put(
                        row.getId(),
                        DecimalInput.id(selection.streamProfileId(), "streamProfileId"));
            }
        }
        if (rows.isEmpty()) return new Resolved(List.of(), List.of());
        var automatic =
                profiles
                        .statusRepresentatives(
                                rows.values().stream().map(CameraChannelEntity::getId).toList())
                        .stream()
                        .collect(
                                Collectors.toMap(
                                        CameraProfileEntity::getChannelId, Function.identity()));
        var explicit =
                explicitProfiles.isEmpty()
                        ? Map.<Long, CameraProfileEntity>of()
                        : profiles.selectByIds(explicitProfiles.values()).stream()
                                .collect(
                                        Collectors.toMap(
                                                CameraProfileEntity::getId, Function.identity()));
        var chosen = new ArrayList<CameraProfileEntity>();
        for (var row : rows.values()) {
            if (explicitProfiles.containsKey(row.getId())) {
                var profile = explicit.get(explicitProfiles.get(row.getId()));
                if (profile == null
                        || !Boolean.TRUE.equals(profile.getEnabled())
                        || !profile.getChannelId().equals(row.getId()))
                    throw BusinessException.error(ErrorCode.CONFLICT)
                            .diagnosed("PLAN_PROFILE_MISSING");
                chosen.add(profile);
            } else if (automatic.containsKey(row.getId())) chosen.add(automatic.get(row.getId()));
        }
        return new Resolved(List.copyOf(rows.values()), List.copyOf(chosen));
    }

    private static String deviceKey(CameraChannelEntity row) {
        return row.getDeviceId() == null ? "c" + row.getId() : "d" + row.getDeviceId();
    }
}
