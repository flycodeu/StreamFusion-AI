package com.streamfusion.platform.camera.service;

import static com.streamfusion.platform.camera.service.CameraAssetRules.invalid;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.mapper.CameraDeviceMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraDeviceEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.util.List;
import java.util.Objects;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

/** Relationship checks and entity reads; write locks join the caller's transaction. */
@Service
@RequiredArgsConstructor
public class CameraAssetRelations {
    private final CameraProfileMapper profiles;
    private final CameraDeviceMapper devices;
    private final CameraSourceService sources;
    private final CameraLocatorService locators;

    void requireManual(CameraChannelEntity camera) {
        if (!"MANUAL".equals(camera.getMappingOrigin()))
            throw BusinessException.error(ErrorCode.CONFLICT);
        sources.requireManualSource(camera.getSourceId(), null);
    }

    void validateDefault(long cameraId, long profileId, Long excludedId) {
        CameraProfileEntity selected = profile(cameraId, profileId, false);
        if (Objects.equals(excludedId, profileId)
                || !selected.getEnabled()
                || !locators.valid(profileId)) throw invalid("defaultPreviewProfileId");
    }

    CameraProfileEntity profile(long cameraId, long profileId, boolean lock) {
        var query =
                new LambdaQueryWrapper<CameraProfileEntity>()
                        .eq(CameraProfileEntity::getId, profileId)
                        .eq(CameraProfileEntity::getChannelId, cameraId);
        if (lock) query.last("FOR UPDATE");
        CameraProfileEntity profile = profiles.selectOne(query);
        if (profile == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return profile;
    }

    CameraDeviceEntity device(long id, boolean lock) {
        var query = new LambdaQueryWrapper<CameraDeviceEntity>().eq(CameraDeviceEntity::getId, id);
        if (lock) query.last("FOR UPDATE");
        CameraDeviceEntity device = devices.selectOne(query);
        if (device == null) throw BusinessException.error(ErrorCode.NOT_FOUND);
        return device;
    }

    List<CameraProfileEntity> profiles(long cameraId) {
        return profiles.selectList(
                new LambdaQueryWrapper<CameraProfileEntity>()
                        .eq(CameraProfileEntity::getChannelId, cameraId)
                        .orderByAsc(CameraProfileEntity::getId));
    }
}
