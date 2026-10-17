package com.streamfusion.platform.camera.service;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.mapper.CameraDeviceMapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class CameraAssetRelationsTest {
    private final CameraProfileMapper profiles = mock(CameraProfileMapper.class);
    private final CameraDeviceMapper devices = mock(CameraDeviceMapper.class);
    private final CameraSourceService sources = mock(CameraSourceService.class);
    private final CameraLocatorService locators = mock(CameraLocatorService.class);
    private final CameraAssetRelations relations =
            new CameraAssetRelations(profiles, devices, sources, locators);

    @Test
    void catalogManagedChannelCannotEnterManualLocatorEditing() {
        var camera = new CameraChannelEntity();
        camera.setSourceId(11L);
        camera.setMappingOrigin("CATALOG");

        assertThatThrownBy(() -> relations.requireManual(camera))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> assertThat(error.code()).isEqualTo(ErrorCode.CONFLICT));

        verifyNoInteractions(sources, locators);
    }

    @Test
    void manualChannelStillRechecksItsSourceEligibility() {
        var camera = new CameraChannelEntity();
        camera.setSourceId(11L);
        camera.setMappingOrigin("MANUAL");
        doThrow(BusinessException.error(ErrorCode.CONFLICT))
                .when(sources)
                .requireManualSource(11L, null);

        assertThatThrownBy(() -> relations.requireManual(camera))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> assertThat(error.code()).isEqualTo(ErrorCode.CONFLICT));

        verify(sources).requireManualSource(11L, null);
    }

    @ParameterizedTest
    @ValueSource(strings = {"EXCLUDED", "DISABLED", "INVALID_LOCATOR"})
    void defaultMustRemainEnabledResolvableAndDifferentFromTheRemovedProfile(String condition) {
        var profile = new CameraProfileEntity();
        profile.setId(101L);
        profile.setEnabled(!"DISABLED".equals(condition));
        when(profiles.selectOne(any())).thenReturn(profile);
        if ("INVALID_LOCATOR".equals(condition)) when(locators.valid(101L)).thenReturn(false);
        Long excluded = "EXCLUDED".equals(condition) ? 101L : null;

        assertThatThrownBy(() -> relations.validateDefault(1L, 101L, excluded))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> {
                            assertThat(error.code()).isEqualTo(ErrorCode.VALIDATION_ERROR);
                            assertThat(error.safeDetails())
                                    .isEqualTo(
                                            new ValidationDetails(
                                                    java.util.List.of(
                                                            ValidationDetails.FieldError.invalid(
                                                                    "defaultPreviewProfileId"))));
                        });
    }

    @Test
    void eligibleDefaultIsAcceptedWithoutMutatingAnAsset() {
        var profile = new CameraProfileEntity();
        profile.setId(101L);
        profile.setEnabled(true);
        when(profiles.selectOne(any())).thenReturn(profile);
        when(locators.valid(101L)).thenReturn(true);

        relations.validateDefault(1L, 101L, 102L);

        verify(profiles).selectOne(any());
        verify(locators).valid(101L);
        verifyNoMoreInteractions(profiles, locators);
        verifyNoInteractions(sources, devices);
    }

    @Test
    void missingDefaultProfileKeepsNotFoundWithoutCheckingItsLocator() {
        assertThatThrownBy(() -> relations.validateDefault(1L, 101L, null))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> assertThat(error.code()).isEqualTo(ErrorCode.NOT_FOUND));

        verifyNoInteractions(locators);
    }
}
