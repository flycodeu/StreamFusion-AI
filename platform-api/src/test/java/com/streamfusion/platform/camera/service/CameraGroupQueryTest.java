package com.streamfusion.platform.camera.service;

import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.streamfusion.platform.camera.mapper.*;
import com.streamfusion.platform.camera.pojo.entity.CameraChannelEntity;
import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import com.streamfusion.platform.common.exception.BusinessException;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Arrays;
import java.util.List;
import org.junit.jupiter.api.Test;

class CameraGroupQueryTest {
    private final CameraGroupMapper groups = mock(CameraGroupMapper.class);
    private final CameraChannelMapper channels = mock(CameraChannelMapper.class);
    private final CameraScopeMapper scopes = mock(CameraScopeMapper.class);
    private final CameraAccessService access = mock(CameraAccessService.class);
    private final Instant observedAt = Instant.parse("2026-10-17T02:00:00Z");
    private final CameraGroupQuery queries =
            new CameraGroupQuery(
                    groups, channels, scopes, access, Clock.fixed(observedAt, ZoneOffset.UTC));

    @Test
    void ordinaryProjectionReusesOneSnapshotAndShowsOnlyAncestorsOfVisibleCounts() {
        ordinary();
        var rows = queries.visibleTree();
        assertThat(rows).extracting(row -> row.groupId()).containsExactly("1", "2", "3");
        assertThat(rows).extracting(row -> row.visibleCameraCount()).containsExactly(3L, 3L, 1L);
        assertThat(rows).extracting(row -> row.hasChildren()).containsExactly(true, true, false);
        assertThat(rows).allSatisfy(row -> assertThat(row.countObservedAt()).isEqualTo(observedAt));
        verify(groups).allBounded();
        verify(scopes).visibleGroupCounts(any());
        verify(scopes, never()).visibleGroups(any());
        verifyNoInteractions(channels);
    }

    @Test
    void nameSearchIgnoresParentFilterButKeepsScopeAndAncestorCounts() {
        ordinary();
        var page = queries.page(null, "LEAF", 1, 10);
        assertThat(page.getTotal()).isEqualTo(1);
        assertThat(page.getItems())
                .singleElement()
                .satisfies(
                        row -> {
                            assertThat(row.groupId()).isEqualTo("3");
                            assertThat(row.visibleCameraCount()).isEqualTo(1);
                            assertThat(row.hasChildren()).isFalse();
                        });
        verify(groups).allBounded();
        verify(scopes).visibleGroupCounts(any());
    }

    @Test
    void groupRootsPreserveCallerOrderAndDoNotLoadForAnEmptySelection() {
        assertThat(queries.queryGroupIds(List.of(), true)).isEmpty();
        verifyNoInteractions(groups);
        when(groups.allBounded()).thenReturn(treeRows());
        assertThat(queries.queryGroupIds(List.of(2L, 99L, 1L, 2L), true))
                .containsExactly(2L, 3L, 1L);
        verify(groups).allBounded();
    }

    @Test
    void placementProjectionUsesTheSuppliedTreeAndRetainsDisabledAndPendingMembers() {
        var pending = member(12, null);
        pending.setLifecycle("PENDING_ASSIGNMENT");
        var disabled = member(11, 2L);
        disabled.setLifecycle("DISABLED");
        var rows =
                queries.devicePlacements(
                        List.of(member(10, 2L), disabled, pending),
                        new CameraGroupTree(treeRows()));
        assertThat(rows)
                .containsExactly(
                        new com.streamfusion.platform.camera.pojo.vo.CameraDeviceMovePreviewVo
                                .Placement("2", "Root / Child", 2),
                        new com.streamfusion.platform.camera.pojo.vo.CameraDeviceMovePreviewVo
                                .Placement(null, "待归档", 1));
        verifyNoInteractions(groups, channels, scopes, access);
    }

    @Test
    void invalidPagingStopsBeforeActorOrSnapshotReadsAndPathsKeepNullOptional() {
        assertThatThrownBy(() -> queries.page(null, null, 0, 100))
                .isInstanceOf(BusinessException.class);
        verifyNoInteractions(groups, channels, scopes, access);
        when(groups.allBounded()).thenReturn(treeRows());
        assertThat(queries.paths(Arrays.asList(2L, null, 3L)))
                .containsEntry(2L, "Root / Child")
                .containsEntry(3L, "Root / Child / Leaf")
                .hasSize(2);
        verify(groups).allBounded();
    }

    private void ordinary() {
        var actor = new CameraAccessService.Actor(7L, false);
        var visibility = new CameraAccessService.Visibility(false, 7L, List.of(2L, 3L));
        when(access.readActor()).thenReturn(actor);
        when(access.visibility(eq(actor), any(CameraGroupTree.class))).thenReturn(visibility);
        when(groups.allBounded()).thenReturn(treeRows());
        when(scopes.visibleGroupCounts(visibility)).thenReturn(List.of(count(2, 2), count(3, 1)));
    }

    private static List<CameraGroupEntity> treeRows() {
        return List.of(
                group(1, null, "Root"),
                group(2, 1L, "Child"),
                group(3, 2L, "Leaf"),
                group(4, null, "Hidden"),
                group(5, null, "Empty"));
    }

    private static CameraGroupEntity group(long id, Long parent, String name) {
        var row = new CameraGroupEntity();
        row.setId(id);
        row.setParentId(parent);
        row.setName(name);
        row.setSortOrder(0);
        row.setVersion(0L);
        return row;
    }

    private static CameraGroupCountRow count(long group, long count) {
        var row = new CameraGroupCountRow();
        row.setGroupId(group);
        row.setCameraCount(count);
        return row;
    }

    private static CameraChannelEntity member(long id, Long group) {
        var row = new CameraChannelEntity();
        row.setId(id);
        row.setGroupId(group);
        row.setLifecycle("ENABLED");
        return row;
    }
}
