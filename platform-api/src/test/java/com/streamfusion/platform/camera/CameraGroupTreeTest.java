package com.streamfusion.platform.camera;

import static org.assertj.core.api.Assertions.*;

import com.streamfusion.platform.camera.pojo.entity.CameraGroupEntity;
import com.streamfusion.platform.camera.service.CameraGroupTree;
import com.streamfusion.platform.common.exception.BusinessException;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;

class CameraGroupTreeTest {
    @Test
    void expandsDynamicDescendantsAndRetainsRootNavigation() {
        var tree =
                new CameraGroupTree(
                        List.of(group(1, null), group(2, 1L), group(3, 2L), group(4, null)));
        assertThat(tree.descendants(List.of(1L, 2L))).containsExactly(1L, 2L, 3L);
        assertThat(tree.ancestors(3L)).containsExactly(3L, 2L, 1L);
        assertThat(tree.path(3L)).isEqualTo("区域1 / 区域2 / 区域3");
        assertThat(tree.descendants(List.of(999L))).isEmpty();
    }

    @Test
    void rejectsMovingIntoItsDescendantAndChecksEntireSubtreeDepth() {
        var rows = new ArrayList<CameraGroupEntity>();
        for (long id = 1; id <= 16; id++) rows.add(group(id, id == 1 ? null : id - 1));
        rows.add(group(100, null));
        rows.add(group(101, 100L));
        var tree = new CameraGroupTree(rows);
        assertThatThrownBy(() -> tree.validateMove(1L, 4L)).isInstanceOf(BusinessException.class);
        assertThatThrownBy(() -> tree.validateMove(100L, 15L))
                .isInstanceOf(BusinessException.class);
        assertThatCode(() -> tree.validateMove(100L, 14L)).doesNotThrowAnyException();
        assertThatThrownBy(() -> tree.validateMove(null, 16L))
                .isInstanceOf(BusinessException.class);
    }

    @Test
    void malformedCyclesAndOversizedTreesFailClosed() {
        var cycle = new CameraGroupTree(List.of(group(1, 2L), group(2, 1L)));
        assertThatThrownBy(() -> cycle.ancestors(1L)).isInstanceOf(BusinessException.class);
        var rows = new ArrayList<CameraGroupEntity>();
        for (long id = 1; id <= 1001; id++) rows.add(group(id, null));
        assertThatThrownBy(() -> new CameraGroupTree(rows)).isInstanceOf(BusinessException.class);
    }

    private static CameraGroupEntity group(long id, Long parent) {
        var group = new CameraGroupEntity();
        group.setId(id);
        group.setParentId(parent);
        group.setName("区域" + id);
        group.setVersion(0L);
        return group;
    }
}
