package com.streamfusion.platform.camera.status;

import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraCryptoService;
import com.streamfusion.platform.camera.service.CameraRtspTargetService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.validation.DecimalInput;
import io.swagger.v3.oas.annotations.media.Schema;
import java.util.ArrayList;
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
            @Schema(description = "视频分组ID，仅GROUPS范围填写，包含子分组") List<String> groupIds) {}

    public record Target(
            @Schema(description = "设备或独立通道的分组键") String groupKey,
            @Schema(description = "目标名称") String name,
            @Schema(description = "不可检测的原因码，可检测时为空") String reasonCode) {}

    public record Preview(
            @Schema(description = "范围内各目标的配置核对结果") List<Target> targets,
            @Schema(description = "范围是否超过单个任务2000个目标的上限") boolean limitExceeded) {}

    @Transactional(readOnly = true)
    public Preview preview(Query query) {
        if (query == null || query.scope() == null) throw invalid();
        var actor = access.readActor();
        List<String> keys;
        List<Long> selectedGroups = null;
        List<Long> fixedChannels = null;
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
                if (selectedGroups.isEmpty()) return new Preview(List.of(), false);
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
                fixedChannels =
                        targets.forPlan(plan.getId()).stream()
                                .map(CameraStatusPlanTargetEntity::getCameraId)
                                .toList();
                if (keys.size() != plan.getTargetCount())
                    throw BusinessException.error(ErrorCode.CONFLICT);
            }
            default -> throw invalid();
        }
        if (keys != null && keys.isEmpty()) return new Preview(List.of(), false);
        var rows =
                fixedChannels != null
                        ? channels.statusSelectedChannels(fixedChannels, access.visibility(actor))
                        : selectedGroups == null
                                ? channels.statusDeviceRepresentatives(
                                        keys, access.visibility(actor), 2001)
                                : channels.statusGroupRepresentatives(
                                        selectedGroups, access.visibility(actor), 2001);
        // Never silently present a truncated all-device scope as a complete patrol.
        if (rows.size() > 2000) return new Preview(List.of(), true);
        var chosen =
                rows.isEmpty()
                        ? List.<CameraProfileEntity>of()
                        : profiles.statusRepresentatives(
                                rows.stream().map(row -> row.getId()).toList());
        var byChannel =
                chosen.stream()
                        .collect(
                                Collectors.toMap(
                                        profile -> profile.getChannelId(), Function.identity()));
        var inspections = rtsp.inspectBatch(rows, chosen, crypto.activeKeyId(), Map.of()).current();
        var result = new ArrayList<Target>();
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
            result.add(new Target(key, row.getName(), reason));
        }
        if (keys != null) {
            var present = result.stream().map(Target::groupKey).collect(Collectors.toSet());
            for (String key : keys)
                if (!present.contains(key)) result.add(new Target(key, key, "TARGET_UNAVAILABLE"));
        }
        return new Preview(List.copyOf(result), false);
    }

    private static BusinessException invalid() {
        return BusinessException.error(ErrorCode.VALIDATION_ERROR);
    }
}
