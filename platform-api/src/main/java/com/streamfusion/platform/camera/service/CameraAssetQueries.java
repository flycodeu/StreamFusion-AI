package com.streamfusion.platform.camera.service;

import static com.streamfusion.platform.camera.service.CameraAssetRules.invalid;

import com.baomidou.mybatisplus.core.metadata.IPage;
import com.streamfusion.platform.camera.mapper.CameraAssetRow;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.pojo.dto.CameraQueryDto;
import com.streamfusion.platform.camera.pojo.entity.CameraDeviceEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.vo.CameraAddressVo;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceGroupVo;
import com.streamfusion.platform.camera.pojo.vo.CameraDeviceVo;
import com.streamfusion.platform.camera.pojo.vo.CameraProfileVo;
import com.streamfusion.platform.camera.pojo.vo.CameraVo;
import com.streamfusion.platform.camera.service.CameraAccessService.Actor;
import com.streamfusion.platform.camera.status.CameraStatusQueryService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.time.Instant;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/**
 * Read projections and request-local display batches; transaction boundaries stay on the facade.
 */
@Service
@RequiredArgsConstructor
public class CameraAssetQueries {
    private static final ZoneId BUSINESS_ZONE = ZoneId.of("Asia/Shanghai");
    private final CameraChannelMapper channels;
    private final CameraAccessService access;
    private final CameraSourceService sources;
    private final CameraLocatorService locators;
    private final CameraGroupService groups;
    private final CameraStatusQueryService status;
    private final CameraAssetRelations relations;

    PageResultVo<CameraVo> page(CameraQueryDto query) {
        if (query == null) throw invalid("query");
        Actor actor = access.readActor();
        var filter = queryFilter(query);
        return channelPage(
                channels.pageVisible(query.toPage(), filter, access.visibility(actor)), actor);
    }

    private CameraChannelMapper.Filter queryFilter(CameraQueryDto query) {
        if (query == null) throw invalid("query");
        String name = CameraAssetRules.text(query.getName(), 128, "name", true);
        if (name != null) name = name.replace("!", "!!").replace("%", "!%").replace("_", "!_");
        Long sourceId = optionalId(query.getSourceId(), "sourceId");
        Long groupId = optionalId(query.getGroupId(), "groupId");
        if (query.getLifecycle() != null
                && !CameraAssetRules.LIFECYCLES.contains(query.getLifecycle()))
            throw invalid("lifecycle");
        if (query.getAdapterType() != null && !sources.supportsAdapter(query.getAdapterType()))
            throw invalid("adapterType");
        return new CameraChannelMapper.Filter(
                name,
                sourceId,
                query.getAdapterType(),
                query.getLifecycle(),
                groupId == null
                        ? null
                        : groups.queryGroupIds(groupId, query.isIncludeDescendants()));
    }

    PageResultVo<CameraDeviceGroupVo> deviceGroups(CameraQueryDto query) {
        Actor actor = access.readActor();
        var filter = queryFilter(query);
        var visibility = access.visibility(actor);
        var page = channels.pageDeviceGroups(query.toPage(), filter, visibility);
        var addresses =
                sources.displayAddresses(
                        page.getRecords().stream().map(row -> row.getSourceId()).toList());
        var summaries = status.deviceSummaries(page.getRecords(), visibility);
        var paths =
                groups.paths(
                        page.getRecords().stream()
                                .map(row -> row.getGroupId())
                                .filter(Objects::nonNull)
                                .distinct()
                                .toList());
        return PageResultVo.from(
                page,
                page.getRecords().stream()
                        .map(
                                row ->
                                        new CameraDeviceGroupVo(
                                                row.getGroupKey(),
                                                row.getName(),
                                                row.isIdentified(),
                                                row.getManufacturer(),
                                                row.getVendorHint(),
                                                row.getModel(),
                                                row.getSourceDisplayName(),
                                                row.getSourceType(),
                                                row.getConnectionCategory(),
                                                addresses.get(row.getSourceId()),
                                                summaries.get(row.getGroupKey()),
                                                row.getChannelCount(),
                                                row.getEnabledCount(),
                                                row.getDisabledCount(),
                                                row.getPendingCount(),
                                                row.getGroupCount(),
                                                row.getGroupCount() == 1
                                                        ? paths.get(row.getGroupId())
                                                        : null))
                        .toList());
    }

    PageResultVo<CameraVo> deviceChannels(String groupKey, CameraQueryDto query) {
        if (groupKey == null || !groupKey.matches("[dc][1-9][0-9]{0,18}"))
            throw invalid("groupKey");
        DecimalInput.id(groupKey.substring(1), "groupKey");
        Actor actor = access.readActor();
        var filter = queryFilter(query);
        return channelPage(
                channels.pageDeviceChannels(
                        query.toPage(), groupKey, filter, access.visibility(actor)),
                actor);
    }

    /** Both channel lists batch shared display data once per page, without loading profiles. */
    private PageResultVo<CameraVo> channelPage(IPage<CameraAssetRow> page, Actor actor) {
        var rows = page.getRecords();
        var addresses =
                sources.displayAddresses(rows.stream().map(CameraAssetRow::getSourceId).toList());
        var paths =
                groups.paths(
                        rows.stream()
                                .map(CameraAssetRow::getGroupId)
                                .filter(Objects::nonNull)
                                .distinct()
                                .toList());
        return PageResultVo.from(
                page,
                rows.stream()
                        .map(
                                row ->
                                        view(
                                                row,
                                                actor,
                                                paths.get(row.getGroupId()),
                                                null,
                                                addresses.get(row.getSourceId())))
                        .toList());
    }

    CameraVo detail(String cameraId) {
        return detail(DecimalInput.id(cameraId, "cameraId"), access.readActor());
    }

    List<CameraProfileVo> profiles(String cameraId) {
        Actor actor = access.readActor();
        long id = DecimalInput.id(cameraId, "cameraId");
        access.requireVisible(id, actor);
        return relations.profiles(id).stream().map(profile -> profileView(profile, actor)).toList();
    }

    CameraDeviceVo device(String deviceId) {
        Actor actor = access.readActor();
        access.requireSuper(actor);
        return deviceView(relations.device(DecimalInput.id(deviceId, "deviceId"), false));
    }

    CameraVo detail(long id, Actor actor) {
        CameraAssetRow row = channels.visible(id, access.visibility(actor));
        if (row == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        String path =
                row.getGroupId() == null
                        ? null
                        : groups.paths(List.of(row.getGroupId())).get(row.getGroupId());
        return view(
                row,
                actor,
                path,
                relations.profiles(id).stream()
                        .map(profile -> profileView(profile, actor))
                        .toList(),
                sources.displayAddresses(List.of(row.getSourceId())).get(row.getSourceId()));
    }

    private static CameraVo view(
            CameraAssetRow row,
            Actor actor,
            String path,
            List<CameraProfileVo> items,
            CameraAddressVo address) {
        CameraVo.DeviceSummary device =
                row.getDeviceId() == null
                        ? null
                        : new CameraVo.DeviceSummary(
                                row.getDeviceType(), row.getManufacturer(), row.getModel());
        return new CameraVo(
                row.getId().toString(),
                row.getName(),
                row.getRemark(),
                row.getSourceName(),
                row.getSourceDisplayName(),
                string(row.getGroupId()),
                path,
                row.getSourceType(),
                row.getConnectionCategory(),
                address,
                row.getVendorHint(),
                device,
                row.getLifecycle(),
                string(row.getDefaultPreviewProfileId()),
                row.getVersion().toString(),
                instant(row.getCreatedAt()),
                instant(row.getUpdatedAt()),
                actor.superAdmin() ? row.getSourceId().toString() : null,
                actor.superAdmin() ? string(row.getSourceVersion()) : null,
                actor.superAdmin() ? string(row.getDeviceId()) : null,
                actor.superAdmin() ? row.getExternalChannelKey() : null,
                items);
    }

    CameraProfileVo profileView(CameraProfileEntity value, Actor actor) {
        return new CameraProfileVo(
                value.getId().toString(),
                value.getLabel(),
                value.getUsageHint(),
                value.getUsageOrigin(),
                value.getEnabled(),
                value.getVideoCodec(),
                value.getWidth(),
                value.getHeight(),
                value.getFrameRate(),
                value.getBitrateKbps(),
                value.getAudioCodec(),
                value.getHasAudio(),
                instant(value.getParametersObservedAt()),
                value.getParametersOrigin(),
                value.getVersion().toString(),
                locators.summary(value.getId(), actor.superAdmin()),
                CameraStreamClassification.classify(
                        value.getUsageHint(), value.getUsageOrigin(), value.getSourceLabel()));
    }

    CameraDeviceVo deviceView(CameraDeviceEntity value) {
        var source = sources.requireSource(value.getSourceId());
        String name = value.getLocalName();
        if (name == null || name.isBlank()) {
            name =
                    "DEVICE".equals(source.getConnectionCategory())
                            ? source.getName()
                            : value.getSourceName();
            if (name == null || name.isBlank()) name = "设备 " + value.getId();
        }
        return new CameraDeviceVo(
                value.getId().toString(),
                value.getSourceId().toString(),
                value.getExternalDeviceKey(),
                value.getExternalDeviceRef(),
                value.getDeviceType(),
                value.getSourceName(),
                name,
                value.getLocalName(),
                value.getRemark(),
                value.getManufacturer(),
                value.getModel(),
                value.getSerialNumber(),
                value.getFirmwareVersion(),
                instant(value.getInfoObservedAt()),
                value.getVersion().toString(),
                instant(value.getCreatedAt()),
                instant(value.getUpdatedAt()));
    }

    private static Long optionalId(String value, String field) {
        return value == null ? null : DecimalInput.id(value, field);
    }

    private static String string(Long value) {
        return value == null ? null : value.toString();
    }

    private static Instant instant(LocalDateTime value) {
        return value == null ? null : value.atZone(BUSINESS_ZONE).toInstant();
    }
}
