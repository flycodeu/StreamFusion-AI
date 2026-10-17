package com.streamfusion.platform.media.preview;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import java.util.List;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class PreviewProfileSelectorTest {
    final CameraChannelMapper channels = mock(CameraChannelMapper.class);
    final CameraProfileMapper profiles = mock(CameraProfileMapper.class);
    final PreviewProfileSelector selector = new PreviewProfileSelector(channels, profiles);
    final CameraChannelEntity channel = new CameraChannelEntity();
    final CameraProfileEntity main = profile(20L, "MAIN", true);
    final CameraProfileEntity sub = profile(21L, "SUB", true);

    @BeforeEach
    void setup() {
        channel.setId(10L);
        channel.setLifecycle("ENABLED");
        channel.setDefaultPreviewProfileId(sub.getId());
        when(channels.selectById(10L)).thenReturn(channel);
        when(profiles.selectList(any())).thenReturn(List.of(main, sub));
    }

    @Test
    void explicitProfileAndUsagePrecedeTheConfiguredDefault() {
        assertThat(selector.select(10L, "20", "SUB")).isSameAs(main);
        assertThat(selector.select(10L, null, "MAIN")).isSameAs(main);
        assertThat(selector.select(10L, null, null)).isSameAs(sub);
        assertThat(selector.select(10L, "99", null)).isNull();
    }

    @Test
    void disabledProfileIsNotChosenByDefaultButAnExplicitChoiceRemainsDiagnosable() {
        sub.setEnabled(false);
        assertThat(selector.select(10L, null, "SUB")).isSameAs(main);
        assertThat(selector.select(10L, "21", null)).isSameAs(sub);
        main.setEnabled(false);
        assertThat(selector.select(10L, null, null)).isNull();
    }

    @Test
    void missingOrDisabledChannelIsRejectedBeforeProfileSelection() {
        when(channels.selectById(10L)).thenReturn(null);
        assertThatThrownBy(() -> selector.select(10L, null, null)).hasMessage("CAMERA_NOT_FOUND");
        when(channels.selectById(10L)).thenReturn(channel);
        channel.setLifecycle("DISABLED");
        assertThatThrownBy(() -> selector.select(10L, null, null)).hasMessage("CAMERA_DISABLED");
        verifyNoInteractions(profiles);
    }

    @Test
    void renewalRequiresBothChannelAndSavedProfileToRemainEnabled() {
        when(profiles.selectById(20L)).thenReturn(main);
        assertThat(selector.enabled(10L, 20L)).isSameAs(main);
        main.setEnabled(false);
        assertThat(selector.enabled(10L, 20L)).isNull();
        main.setEnabled(true);
        channel.setLifecycle("DISABLED");
        assertThat(selector.enabled(10L, 20L)).isNull();
    }

    private static CameraProfileEntity profile(long id, String usage, boolean enabled) {
        var profile = new CameraProfileEntity();
        profile.setId(id);
        profile.setChannelId(10L);
        profile.setUsageHint(usage);
        profile.setUsageOrigin("MANUAL");
        profile.setEnabled(enabled);
        return profile;
    }
}
