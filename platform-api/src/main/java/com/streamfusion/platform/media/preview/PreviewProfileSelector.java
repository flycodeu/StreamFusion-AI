package com.streamfusion.platform.media.preview;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.common.validation.DecimalInput;
import org.springframework.stereotype.Component;

/** Selects a saved profile without resolving addresses or allocating media resources. */
@Component
public class PreviewProfileSelector {
    private final CameraChannelMapper channels;
    private final CameraProfileMapper profiles;

    public PreviewProfileSelector(CameraChannelMapper channels, CameraProfileMapper profiles) {
        this.channels = channels;
        this.profiles = profiles;
    }

    CameraProfileEntity select(long cameraId, String profileId, String usage) {
        var channel = channels.selectById(cameraId);
        if (channel == null) throw new PreviewFailure("CAMERA_NOT_FOUND");
        if (!"ENABLED".equals(channel.getLifecycle())) throw new PreviewFailure("CAMERA_DISABLED");
        var values =
                profiles.selectList(
                        new LambdaQueryWrapper<CameraProfileEntity>()
                                .eq(CameraProfileEntity::getChannelId, cameraId)
                                .orderByAsc(CameraProfileEntity::getId));
        if (profileId != null) {
            long id = DecimalInput.id(profileId, "streamProfileId");
            return values.stream().filter(p -> p.getId().equals(id)).findFirst().orElse(null);
        }
        var enabled = values.stream().filter(p -> Boolean.TRUE.equals(p.getEnabled())).toList();
        if (usage != null) {
            var match =
                    enabled.stream()
                            .filter(p -> usage.equals(PreviewCatalogService.usage(p)))
                            .findFirst();
            if (match.isPresent()) return match.get();
        }
        var preferred =
                enabled.stream()
                        .filter(p -> p.getId().equals(channel.getDefaultPreviewProfileId()))
                        .findFirst();
        if (preferred.isPresent()) return preferred.get();
        return enabled.stream()
                .filter(p -> "MAIN".equals(PreviewCatalogService.usage(p)))
                .findFirst()
                .orElse(enabled.isEmpty() ? null : enabled.getFirst());
    }

    CameraProfileEntity enabled(long cameraId, Long profileId) {
        var channel = channels.selectById(cameraId);
        var profile = profiles.selectById(profileId);
        return channel != null
                        && "ENABLED".equals(channel.getLifecycle())
                        && profile != null
                        && Boolean.TRUE.equals(profile.getEnabled())
                ? profile
                : null;
    }
}
