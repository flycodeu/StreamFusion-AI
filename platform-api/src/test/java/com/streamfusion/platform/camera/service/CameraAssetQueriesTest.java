package com.streamfusion.platform.camera.service;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.streamfusion.platform.camera.mapper.CameraAssetRow;
import com.streamfusion.platform.camera.mapper.CameraChannelMapper;
import com.streamfusion.platform.camera.pojo.dto.CameraQueryDto;
import com.streamfusion.platform.camera.pojo.entity.CameraProfileEntity;
import com.streamfusion.platform.camera.pojo.vo.CameraAddressVo;
import com.streamfusion.platform.camera.service.CameraAccessService.Actor;
import com.streamfusion.platform.camera.service.CameraAccessService.Visibility;
import com.streamfusion.platform.camera.status.CameraStatusQueryService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import java.time.Instant;
import java.time.LocalDateTime;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class CameraAssetQueriesTest {
    private final CameraChannelMapper channels = mock(CameraChannelMapper.class);
    private final CameraAccessService access = mock(CameraAccessService.class);
    private final CameraSourceService sources = mock(CameraSourceService.class);
    private final CameraLocatorService locators = mock(CameraLocatorService.class);
    private final CameraGroupService groups = mock(CameraGroupService.class);
    private final CameraStatusQueryService status = mock(CameraStatusQueryService.class);
    private final CameraAssetRelations relations = mock(CameraAssetRelations.class);
    private final CameraAssetQueries queries =
            new CameraAssetQueries(channels, access, sources, locators, groups, status, relations);

    @ParameterizedTest
    @ValueSource(booleans = {false, true})
    void channelListsReuseScopeAndBatchDisplayDataWithoutLoadingProfiles(boolean devicePage) {
        Actor actor = new Actor(7L, false);
        Visibility visibility = new Visibility(false, 7L, List.of(90L, 91L));
        when(access.readActor()).thenReturn(actor);
        when(access.visibility(actor)).thenReturn(visibility);
        var query = new CameraQueryDto();
        query.setName(" Lobby%_! ");
        query.setSourceId("11");
        query.setGroupId("90");
        query.setIncludeDescendants(true);
        query.setAdapterType("RTSP");
        query.setLifecycle("ENABLED");
        when(sources.supportsAdapter("RTSP")).thenReturn(true);
        when(groups.queryGroupIds(90L, true)).thenReturn(List.of(90L, 91L));
        var page = new Page<CameraAssetRow>(2, 20, 31);
        page.setRecords(List.of(row(1L, 11L, 90L), row(2L, 11L, 91L), row(3L, 12L, 90L)));
        var filter =
                new CameraChannelMapper.Filter(
                        "Lobby!%!_!!", 11L, "RTSP", "ENABLED", List.of(90L, 91L));
        if (devicePage) {
            when(channels.pageDeviceChannels(any(), eq("d10"), eq(filter), same(visibility)))
                    .thenReturn(page);
        } else {
            when(channels.pageVisible(any(), eq(filter), same(visibility))).thenReturn(page);
        }
        var address = new CameraAddressVo("RTSP", "192.0.2.11", 554);
        when(sources.displayAddresses(List.of(11L, 11L, 12L))).thenReturn(Map.of(11L, address));
        when(groups.paths(List.of(90L, 91L)))
                .thenReturn(Map.of(90L, "Site / Lobby", 91L, "Site / Yard"));

        var result = devicePage ? queries.deviceChannels("d10", query) : queries.page(query);

        assertThat(result.getPage()).isEqualTo(2);
        assertThat(result.getSize()).isEqualTo(20);
        assertThat(result.getTotal()).isEqualTo(31);
        assertThat(result.getItems()).hasSize(3);
        assertThat(result.getItems().get(0).accessAddress()).isEqualTo(address);
        assertThat(result.getItems().get(1).groupPath()).isEqualTo("Site / Yard");
        assertThat(result.getItems().get(2).accessAddress()).isNull();
        assertThat(result.getItems())
                .allSatisfy(
                        value -> {
                            assertThat(value.profiles()).isNull();
                            assertThat(value.sourceId()).isNull();
                            assertThat(value.sourceVersion()).isNull();
                            assertThat(value.deviceId()).isNull();
                            assertThat(value.externalChannelKey()).isNull();
                        });
        verify(access).visibility(actor);
        verify(sources).displayAddresses(List.of(11L, 11L, 12L));
        verify(groups).paths(List.of(90L, 91L));
        verifyNoInteractions(relations, locators, status);
    }

    @ParameterizedTest
    @ValueSource(booleans = {false, true})
    void detailKeepsIdentityAndLocatorProjectionBoundToTheActor(boolean admin) {
        Actor actor = new Actor(7L, admin);
        Visibility visibility = new Visibility(admin, 7L, List.of());
        when(access.readActor()).thenReturn(actor);
        when(access.visibility(actor)).thenReturn(visibility);
        var row = row(1L, 11L, 90L);
        row.setCreatedAt(LocalDateTime.of(2026, 10, 17, 8, 0));
        when(channels.visible(1L, visibility)).thenReturn(row);
        when(groups.paths(List.of(90L))).thenReturn(Map.of(90L, "Site / Lobby"));
        when(sources.displayAddresses(List.of(11L))).thenReturn(Map.of());
        var profile = new CameraProfileEntity();
        profile.setId(101L);
        profile.setVersion(0L);
        profile.setEnabled(true);
        profile.setUsageHint("MAIN");
        profile.setUsageOrigin("MANUAL");
        when(relations.profiles(1L)).thenReturn(List.of(profile));
        Map<String, Object> locator = Map.of("kind", "RTSP", "valid", true);
        when(locators.summary(101L, admin)).thenReturn(locator);

        var result = queries.detail("1");

        assertThat(result.sourceId()).isEqualTo(admin ? "11" : null);
        assertThat(result.sourceVersion()).isEqualTo(admin ? "3" : null);
        assertThat(result.deviceId()).isEqualTo(admin ? "10" : null);
        assertThat(result.externalChannelKey()).isEqualTo(admin ? "channel:1" : null);
        assertThat(result.deviceSummary()).isNotNull();
        assertThat(result.createdAt()).isEqualTo(Instant.parse("2026-10-17T00:00:00Z"));
        assertThat(result.profiles())
                .singleElement()
                .satisfies(value -> assertThat(value.locatorSummary()).isEqualTo(locator));
        verify(access).visibility(actor);
        verify(locators).summary(101L, admin);
        verifyNoInteractions(status);
    }

    @Test
    void invisibleDetailStopsBeforeAnyProfileOrDisplayHydration() {
        Actor actor = new Actor(7L, false);
        Visibility visibility = new Visibility(false, 7L, List.of());
        when(access.readActor()).thenReturn(actor);
        when(access.visibility(actor)).thenReturn(visibility);

        assertThatThrownBy(() -> queries.detail("1"))
                .isInstanceOfSatisfying(
                        BusinessException.class,
                        error -> assertThat(error.code()).isEqualTo(ErrorCode.NOT_FOUND));

        verify(channels).visible(1L, visibility);
        verifyNoInteractions(relations, groups, sources, locators, status);
    }

    private static CameraAssetRow row(long id, long sourceId, long groupId) {
        var value = new CameraAssetRow();
        value.setId(id);
        value.setName("Camera " + id);
        value.setSourceId(sourceId);
        value.setSourceVersion(3L);
        value.setGroupId(groupId);
        value.setDeviceId(10L);
        value.setExternalChannelKey("channel:" + id);
        value.setVersion(0L);
        return value;
    }
}
