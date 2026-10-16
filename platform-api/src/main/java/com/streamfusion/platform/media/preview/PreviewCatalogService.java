package com.streamfusion.platform.media.preview;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.access.adapter.CameraAccessAdapterRegistry;
import com.streamfusion.platform.camera.mapper.CameraAssetRow;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraDeviceMapper;
import com.streamfusion.platform.camera.mapper.CameraLocatorMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.mapper.CameraSourceMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraDeviceEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraLocatorEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.camera.service.CameraGroupService;
import com.streamfusion.platform.camera.service.CameraStreamClassification;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.stream.Collectors;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class PreviewCatalogService {
    private final CameraAccessService access;
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;
    private final CameraDeviceMapper devices;
    private final CameraGroupService groups;
    private final CameraLocatorMapper locatorRows;
    private final CameraSourceMapper sources;
    private final CameraAccessAdapterRegistry adapters;

    @Transactional(readOnly = true)
    public PageResultVo<PreviewModels.Channel> catalog(PreviewModels.CatalogQuery input) {
        var actor = access.readActor();
        String name = input.getName();
        if (name != null) {
            name = name.strip();
            if (name.length() > 128 || name.codePoints().anyMatch(Character::isISOControl))
                throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
            name = name.replace("!", "!!").replace("%", "!%").replace("_", "!_");
        }
        var filter = new CameraChannelMapper.Filter(name, null, null, null, null);
        String device = input.getDevice();
        if (device != null) {
            if (!device.matches("[dc][1-9][0-9]{0,18}"))
                throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
            DecimalInput.id(device.substring(1), "device");
        }
        var page =
                device == null
                        ? channels.pageVisible(input.toPage(), filter, access.visibility(actor))
                        : channels.pageDeviceChannels(
                                input.toPage(), device, filter, access.visibility(actor));
        if (page.getRecords().isEmpty()) return PageResultVo.from(page, List.of());
        var ids = page.getRecords().stream().map(CameraAssetRow::getId).toList();
        var profileMap =
                profiles
                        .selectList(
                                new LambdaQueryWrapper<CameraProfileEntity>()
                                        .in(CameraProfileEntity::getChannelId, ids)
                                        .orderByAsc(CameraProfileEntity::getId))
                        .stream()
                        .collect(Collectors.groupingBy(CameraProfileEntity::getChannelId));
        var profileIds =
                profileMap.values().stream()
                        .flatMap(List::stream)
                        .map(CameraProfileEntity::getId)
                        .toList();
        Map<Long, CameraLocatorEntity> locatorMap =
                profileIds.isEmpty()
                        ? Map.of()
                        : locatorRows.selectByIds(profileIds).stream()
                                .collect(
                                        Collectors.toMap(
                                                CameraLocatorEntity::getProfileId, l -> l));
        var enabledSources =
                sources
                        .selectByIds(
                                page.getRecords().stream()
                                        .map(CameraAssetRow::getSourceId)
                                        .distinct()
                                        .toList())
                        .stream()
                        .filter(s -> Boolean.TRUE.equals(s.getEnabled()))
                        .map(s -> s.getId())
                        .collect(Collectors.toSet());
        var deviceIds =
                page.getRecords().stream()
                        .map(CameraAssetRow::getDeviceId)
                        .filter(Objects::nonNull)
                        .distinct()
                        .toList();
        Map<Long, CameraDeviceEntity> deviceMap =
                deviceIds.isEmpty()
                        ? Map.of()
                        : devices.selectByIds(deviceIds).stream()
                                .collect(Collectors.toMap(CameraDeviceEntity::getId, d -> d));
        var paths =
                groups.paths(
                        page.getRecords().stream()
                                .map(CameraAssetRow::getGroupId)
                                .filter(Objects::nonNull)
                                .distinct()
                                .toList());
        return PageResultVo.from(
                page,
                page.getRecords().stream()
                        .map(
                                row -> {
                                    var d =
                                            row.getDeviceId() == null
                                                    ? null
                                                    : deviceMap.get(row.getDeviceId());
                                    String deviceName = row.getName();
                                    if (d != null) {
                                        deviceName =
                                                d.getLocalName() != null
                                                                && !d.getLocalName().isBlank()
                                                        ? d.getLocalName()
                                                        : "DEVICE"
                                                                        .equals(
                                                                                row
                                                                                        .getConnectionCategory())
                                                                ? row.getSourceDisplayName()
                                                                : d.getSourceName();
                                        if (deviceName == null || deviceName.isBlank())
                                            deviceName = "设备 " + d.getId();
                                    }
                                    return new PreviewModels.Channel(
                                            row.getId().toString(),
                                            row.getName(),
                                            (row.getDeviceId() == null
                                                    ? "c" + row.getId()
                                                    : "d" + row.getDeviceId()),
                                            deviceName,
                                            row.getGroupId() == null
                                                    ? null
                                                    : row.getGroupId().toString(),
                                            row.getGroupId() == null
                                                    ? null
                                                    : paths.get(row.getGroupId()),
                                            row.getLifecycle(),
                                            profileMap.getOrDefault(row.getId(), List.of()).stream()
                                                    .map(
                                                            p ->
                                                                    profile(
                                                                            p,
                                                                            enabledSources.contains(
                                                                                            p
                                                                                                    .getSourceId())
                                                                                    && configured(
                                                                                            locatorMap
                                                                                                    .get(
                                                                                                            p
                                                                                                                    .getId()))))
                                                    .toList());
                                })
                        .toList());
    }

    private boolean configured(CameraLocatorEntity l) {
        if (l == null || !adapters.methods().contains(l.getLocatorKind())) return false;
        byte[] ciphertext =
                "RTSP".equals(l.getLocatorKind())
                        ? l.getRtspSecretCiphertext()
                        : l.getProtocolSecretCiphertext();
        return ciphertext != null && ciphertext.length > 0;
    }

    private PreviewModels.Profile profile(CameraProfileEntity p, boolean configured) {
        return new PreviewModels.Profile(
                p.getId().toString(),
                p.getLabel(),
                usage(p),
                Boolean.TRUE.equals(p.getEnabled()),
                configured,
                p.getVideoCodec(),
                p.getWidth(),
                p.getHeight(),
                p.getFrameRate(),
                p.getBitrateKbps());
    }

    static String usage(CameraProfileEntity p) {
        return CameraStreamClassification.classify(
                        p.getUsageHint(), p.getUsageOrigin(), p.getSourceLabel())
                .usageHint();
    }
}
